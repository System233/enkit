package astore

import (
	"context"
	"github.com/System233/enkit/astore/rpc/astore"
	"github.com/System233/enkit/lib/client"
)

func (c *Client) Note(uid string, note string) ([]*astore.Artifact, error) {
	req := &astore.NoteRequest{Uid: uid, Note: note}

	resp, err := c.client.Note(context.TODO(), req)
	if err != nil {
		return nil, client.NiceError(err, "could not annotate uid %s", err)
	}
	return resp.Artifact, nil
}
