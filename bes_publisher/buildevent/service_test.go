package buildevent

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"

	"cloud.google.com/go/pubsub"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	bpb "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/System233/enkit/lib/errdiff"
	"github.com/System233/enkit/lib/testutil"
	bes "github.com/System233/enkit/third_party/bazel/buildeventstream"
)

func TestServicePublishLifecycleEvent(t *testing.T) {
	testCases := []struct {
		desc    string
		req     *bpb.PublishLifecycleEventRequest
		wantErr string
	}{
		{
			desc: "no error on any call",
			req:  &bpb.PublishLifecycleEventRequest{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := context.Background()
			service := &Service{}

			_, gotErr := service.PublishLifecycleEvent(ctx, tc.req)

			errdiff.Check(t, gotErr, tc.wantErr)
		})
	}
}

func anypbOrDie(msg proto.Message) *anypb.Any {
	a, err := anypb.New(msg)
	if err != nil {
		panic(err)
	}
	return a
}

func wrapBesMessages(msgs []*bes.BuildEvent) []*bpb.PublishBuildToolEventStreamRequest {
	var wrapped []*bpb.PublishBuildToolEventStreamRequest
	for i, msg := range msgs {
		wrapped = append(wrapped, &bpb.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &bpb.OrderedBuildEvent{
				SequenceNumber: int64(i),
				Event: &bpb.BuildEvent{
					Event: &bpb.BuildEvent_BazelEvent{
						BazelEvent: anypbOrDie(msg),
					},
				},
			},
		})
	}
	return wrapped
}

func TestPublishBuildToolEventStream(t *testing.T) {
	testCases := []struct {
		desc          string
		events        []*bes.BuildEvent
		streamSendErr error
		streamRecvErr error

		wantMessages []*pubsub.Message
		wantErr      string
	}{
		{
			desc:    "no events",
			events:  []*bes.BuildEvent{},
			wantErr: "",
		},
		{
			desc: "normal build",
			events: []*bes.BuildEvent{
				{
					Payload: &bes.BuildEvent_Started{
						Started: &bes.BuildStarted{
							Uuid: "d9b5cec0-c1e6-428c-8674-a74194b27447",
						},
					},
				},
				{
					Payload: &bes.BuildEvent_BuildMetadata{
						BuildMetadata: &bes.BuildMetadata{
							Metadata: map[string]string{
								"ROLE":              "interactive",
								"build_tag:foo":     "bar",
								"not_build_tag:baz": "quux",
							},
						},
					},
				},
				{
					Payload: &bes.BuildEvent_WorkspaceStatus{
						WorkspaceStatus: &bes.WorkspaceStatus{
							Item: []*bes.WorkspaceStatus_Item{
								{Key: "GIT_USER", Value: "jmcclane"},
							},
						},
					},
				},
				{
					Id: &bes.BuildEventId{
						Id: &bes.BuildEventId_TestResult{
							TestResult: &bes.BuildEventId_TestResultId{
								Label: "//foo/bar:baz_test",
								Run:   1,
							},
						},
					},
					Payload: &bes.BuildEvent_TestResult{
						TestResult: &bes.TestResult{
							Status:        bes.TestStatus_PASSED,
							CachedLocally: false,
						},
					},
				},
				{
					Payload: &bes.BuildEvent_Finished{
						Finished: &bes.BuildFinished{
							ExitCode: &bes.BuildFinished_ExitCode{
								Name: "SUCCESS",
								Code: 0,
							},
						},
					},
				},
				{
					Payload: &bes.BuildEvent_BuildMetrics{
						BuildMetrics: &bes.BuildMetrics{
							BuildGraphMetrics: &bes.BuildMetrics_BuildGraphMetrics{
								ActionCount: 3,
							},
						},
					},
				},
			},
			wantMessages: []*pubsub.Message{
				{
					Data: []byte(`{"started":{"uuid":"d9b5cec0-c1e6-428c-8674-a74194b27447"}}`),
					Attributes: map[string]string{
						"inv_id": "d9b5cec0-c1e6-428c-8674-a74194b27447",
					},
				},
				{
					Data: []byte(`{"buildMetadata":{"metadata":{"ROLE":"interactive","build_tag:foo":"bar","not_build_tag:baz":"quux"}}}`),
					Attributes: map[string]string{
						"inv_id":   "d9b5cec0-c1e6-428c-8674-a74194b27447",
						"inv_type": "interactive",
						"bt__foo":  "bar",
					},
				},
				{
					Data: []byte(`{"workspaceStatus":{"item":[{"key":"GIT_USER", "value":"jmcclane"}]}}`),
					Attributes: map[string]string{
						"inv_id":   "d9b5cec0-c1e6-428c-8674-a74194b27447",
						"inv_type": "interactive",
						"bt__foo":  "bar",
					},
				},
				{
					Data: []byte(`{"id":{"testResult":{"label":"//foo/bar:baz_test", "run":1}}, "testResult":{"status":"PASSED"}}`),
					Attributes: map[string]string{
						"inv_id":   "d9b5cec0-c1e6-428c-8674-a74194b27447",
						"inv_type": "interactive",
						"bt__foo":  "bar",
					},
				},
				{
					Data: []byte(`{"finished":{"exitCode":{"name":"SUCCESS"}}}`),
					Attributes: map[string]string{
						"inv_id":   "d9b5cec0-c1e6-428c-8674-a74194b27447",
						"inv_type": "interactive",
						"result":   "SUCCESS",
						"bt__foo":  "bar",
					},
				},
				{
					Data: []byte(`{"buildMetrics":{"buildGraphMetrics":{"actionCount":3}}}`),
					Attributes: map[string]string{
						"inv_id":   "d9b5cec0-c1e6-428c-8674-a74194b27447",
						"inv_type": "interactive",
						"result":   "SUCCESS",
						"bt__foo":  "bar",
					},
				},
			},
			wantErr: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := context.Background()
			bepEvents := wrapBesMessages(tc.events)

			topic := &mockTopic{}
			service, err := NewService(topic)
			require.NoError(t, err)

			stream := &mockStream{}
			stream.On("Context").Return(ctx)
			stream.On("Send", mock.Anything).Return(tc.streamSendErr)
			for _, event := range bepEvents {
				stream.On("Recv").Return(event, nil).Once()
			}
			if tc.streamRecvErr != nil {
				stream.On("Recv").Return(nil, tc.streamRecvErr).Once()
			} else {
				stream.On("Recv").Return(nil, io.EOF).Once()
			}

			for _, msg := range tc.wantMessages {
				// Need to capture the loop variable, or all the assertions will
				// run against the last element of wantMessages.
				msg := *msg
				topic.On("Publish", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
					sent := args.Get(1).(*pubsub.Message)
					testutil.AssertCmp(t, sent, &msg, cmp.Comparer(bytesEqual), cmpopts.IgnoreUnexported(pubsub.Message{}))
				}).Return(newMockPublishResult(randomMs(10, 100), nil)).Once()
			}

			gotErr := service.PublishBuildToolEventStream(stream)

			errdiff.Check(t, gotErr, tc.wantErr)
		})
	}
}

func bytesEqual(a, b []byte) bool {
	// There's something goofy happening (spaces are added/removed in byte slices to
	// cause tests to always fail)
	// Specifying our own bytes comparison routine that "normalizes" the byte
	// slices fixes this. The tests may be inaccurate - stripping spaces doesn't
	// matter for JSON, but does for any strings within JSON - but hopefully the
	// tests will be "good enough" anyway.
	a = bytes.ReplaceAll(a, []byte(" "), []byte{})
	b = bytes.ReplaceAll(b, []byte(" "), []byte{})
	return reflect.DeepEqual(a, b)
}
