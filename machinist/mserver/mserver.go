package mserver

import (
	"context"
	"net/http"

	"github.com/System233/enkit/lib/server"
	"github.com/System233/enkit/machinist/config"
	mpb "github.com/System233/enkit/machinist/rpc"

	"google.golang.org/grpc"
)

func New(mods ...Modifier) (*ControlPlane, error) {
	kd, err := NewController()
	if err != nil {
		return nil, err
	}
	s := &ControlPlane{
		killChannel:              make(chan error),
		Controller:               kd,
		Common:                   config.DefaultCommonFlags(),
		allRecordsKillChannel:    make(chan struct{}, 1),
		allRecordsKillAckChannel: make(chan struct{}, 1),
	}
	for _, m := range mods {
		if err := m(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

type ControlPlane struct {
	insecure                 bool
	runningServer            *grpc.Server
	allRecordsKillChannel    chan struct{}
	allRecordsKillAckChannel chan struct{}
	killChannel              chan error

	Controller *Controller
	*config.Common
}

func (s *ControlPlane) MachinistCommon() *config.Common {
	return s.Common
}

func (s *ControlPlane) Run() error {
	ctx := context.Background()

	grpcs := grpc.NewServer()
	mpb.RegisterControllerServer(grpcs, s.Controller)
	s.runningServer = grpcs
	go func() {
		s.killChannel <- s.Controller.dnsServer.Run()
	}()
	s.Controller.Init()
	go s.Controller.ServeAllAndInfoRecords(s.allRecordsKillChannel, s.allRecordsKillAckChannel)
	go s.Controller.WriteState()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics_targets", s.Controller.MetricsTargets)

	return server.Run(ctx, mux, grpcs, s.Listener)
}

func (s *ControlPlane) Stop() error {
	s.allRecordsKillChannel <- struct{}{}
	<-s.allRecordsKillAckChannel
	return s.Controller.dnsServer.Stop()
}
