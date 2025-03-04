package astore_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/System233/enkit/astore/client/astore"
	apb "github.com/System233/enkit/astore/rpc/astore"
	"github.com/System233/enkit/lib/client/ccontext"
	"github.com/System233/enkit/lib/logger"
	"github.com/System233/enkit/lib/progress"

	"github.com/stretchr/testify/assert"
)

// TODO(aaahrens): fix client so that its signed urls can depend on an interface for actual e2e testing.
func TestServer(t *testing.T) {
	astoreDescriptor, killFuncs, err := RunAStoreServer()
	if killFuncs != nil {
		defer killFuncs.KillAll()
	}
	assert.Nil(t, err)
	// Running this as test ping feature.
	client := astore.New(astoreDescriptor.Connection)
	res, _, err := client.List("/test", astore.ListOptions{})
	assert.Nil(t, err)
	fmt.Printf("list response is +%v \n", res)
	uploadFiles := []astore.FileToUpload{
		{Local: "./testdata/example.yaml"},
	}

	ctxWithLogger := ccontext.DefaultContext()
	ctxWithLogger.Logger = logger.DefaultLogger{Printer: log.Printf}
	ctxWithLogger.Progress = progress.NewDiscard

	uploadOption := astore.UploadOptions{
		Context: ctxWithLogger,
	}
	u, err := client.Upload(uploadFiles, uploadOption)
	assert.Nil(t, err, "client upload failed with %s", err)

	fmt.Printf("upload is +%v \n", u)
	storeResponse, err := astoreDescriptor.Server.Store(context.Background(), &apb.StoreRequest{})
	assert.Nil(t, err)
	assert.NotEqual(t, "", storeResponse.GetSid())
	assert.NotEqual(t, "", storeResponse.GetUrl())

	resp, err := astoreDescriptor.Server.Commit(context.Background(), &apb.CommitRequest{
		Sid:          storeResponse.GetSid(),
		Architecture: "dwarvenx99",
		Path:         "127.0.0.1:9000/hello/work/example.yaml",
		Note:         "note",
		Tag:          []string{"something"},
	})
	assert.Nil(t, err)

	fmt.Println("finalizing +%v", resp.Artifact)
}
