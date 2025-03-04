package auth

import (
	"context"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/System233/enkit/auth/common"
	apb "github.com/System233/enkit/auth/proto"
	"github.com/System233/enkit/lib/kcerts"
	"github.com/System233/enkit/lib/logger"
	"github.com/System233/enkit/lib/oauth"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"math/rand"
	"sync"
	"time"
)

type Server struct {
	rng                   *rand.Rand
	serverPub, serverPriv *common.Key

	jarlock sync.Mutex
	jars    map[common.Key]*Jar

	authURL   string
	useGroups bool
	limit     time.Duration

	caPrivateKey          kcerts.PrivateKey
	principals            []string
	marshalledCAPublicKey []byte
	userCertTTL           time.Duration
	log                   logger.Logger
}

func (s *Server) HostCertificate(ctx context.Context, request *apb.HostCertificateRequest) (*apb.HostCertificateResponse, error) {
	b, _ := pem.Decode(request.Hostcert)
	if b == nil {
		return nil, errors.New("the public key was empty, or was an invlaid block")
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	cert, err := kcerts.SignPublicKey(s.caPrivateKey, ssh.HostCert, request.Hosts, s.userCertTTL, pubKey)
	if err != nil {
		return nil, err
	}
	return &apb.HostCertificateResponse{
		Capublickey:    s.marshalledCAPublicKey,
		Signedhostcert: ssh.MarshalAuthorizedKey(cert),
	}, nil
}

type Jar struct {
	created time.Time
	channel chan oauth.AuthData
	cancel  context.CancelFunc
}

func (s *Server) GetChannel(cancel context.CancelFunc, pub common.Key) chan oauth.AuthData {
	s.jarlock.Lock()
	defer s.jarlock.Unlock()

	jar := s.jars[pub]
	if jar != nil {
		if jar.cancel != nil {
			jar.cancel()
		}
		jar.cancel = cancel
		return jar.channel
	}

	jar = &Jar{
		created: time.Now(),
		// Hold at least one token in the buffer.
		//
		// This allows a client supllying a token to not block until the token
		// has in facts been consumed.
		channel: make(chan oauth.AuthData, 1),
		cancel:  cancel,
	}
	s.jars[pub] = jar
	return jar.channel
}

func (s *Server) Authenticate(ctx context.Context, req *apb.AuthenticateRequest) (*apb.AuthenticateResponse, error) {
	key, err := common.KeyFromSlice(req.Key)
	if err != nil {
		return nil, err
	}
	resp := &apb.AuthenticateResponse{
		Key: (*s.serverPub)[:],
		Url: fmt.Sprintf("%s/%s", s.authURL, hex.EncodeToString(key[:])),
	}
	return resp, nil
}

func (s *Server) FeedToken(key common.Key, cookie oauth.AuthData) {
	channel := s.GetChannel(nil, key)
	channel <- cookie
}

func (s *Server) Token(ctx context.Context, req *apb.TokenRequest) (*apb.TokenResponse, error) {
	clientPub, err := common.KeyFromURL(req.Url)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	channel := s.GetChannel(cancel, *clientPub)
	select {
	case <-ctx.Done():
		return nil, status.Errorf(codes.Canceled, "context canceled while waiting for authentication")
	case <-time.After(s.limit):
		return nil, status.Errorf(codes.DeadlineExceeded, "timed out waiting for your lazy fingers to complete authentication")

	case authData := <-channel:
		var nonce [common.NonceLength]byte
		if _, err := io.ReadFull(s.rng, nonce[:]); err != nil {
			return nil, status.Errorf(codes.Internal, "could not generate nonce - %s", err)
		}

		// If the ca signer is nil that means the CA was never passed in flags, if the request never sent a public key
		// then so ssh certs will be sent back.
		if s.caPrivateKey == nil || len(req.Publickey) <= 0 {
			return &apb.TokenResponse{
				Nonce: nonce[:],
				Token: box.Seal(nil, []byte(authData.Cookie), &nonce, (*[32]byte)(clientPub), (*[32]byte)(s.serverPriv)),
			}, nil
		}
		// If the ca signer was present, continuing with public keys.
		savedPubKey, _, _, _, err := ssh.ParseAuthorizedKey(req.Publickey)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "PublicKey cannot be parsed as an ssh authorized key - %s", err)
		}
		var certMods []kcerts.CertMod
		effectivePrincipals := append([]string{}, s.principals...)
		effectivePrincipals = append(effectivePrincipals, authData.Creds.Identity.Username)
		effectivePrincipals = append(effectivePrincipals, authData.Creds.Identity.GlobalName())
		if s.useGroups {
			effectivePrincipals = append(effectivePrincipals, authData.Creds.Identity.Groups...)
		}

		for _, i := range authData.Identities {
			effectivePrincipals = append(effectivePrincipals, i.GlobalName())
			if s.useGroups {
				effectivePrincipals = append(effectivePrincipals, i.Groups...)
			}
			certMods = append(certMods, i.CertMod())
		}
		userCert, err := kcerts.SignPublicKey(s.caPrivateKey, ssh.UserCert, effectivePrincipals, s.userCertTTL, savedPubKey, certMods...)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error signing key - %s", err)
		}
		return &apb.TokenResponse{
			Nonce:       nonce[:],
			Token:       box.Seal(nil, []byte(authData.Cookie), &nonce, (*[32]byte)(clientPub), (*[32]byte)(s.serverPriv)),
			Capublickey: s.marshalledCAPublicKey,
			// Always trust the CA for now since the DNS gets resolved behind tunnel and therefore the client doesn't know
			// which to trust.
			Cahosts: []string{"*"},
			Cert:    ssh.MarshalAuthorizedKey(userCert),
		}, nil
	}

	return nil, status.Errorf(codes.Internal, "never reached, clearly")
}
