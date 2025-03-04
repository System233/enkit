package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/System233/enkit/lib/metrics"
	"github.com/System233/enkit/lib/multierror"
	"github.com/System233/enkit/lib/server"
	bes "github.com/System233/enkit/third_party/bazel/buildeventstream" // Allows prototext to automatically decode embedded messages

	"github.com/golang/glog"
	"github.com/golang/protobuf/ptypes"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	bpb "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	fileTooBigErr     = errors.New("File exceeds maximum size allowed")
	maxFileSize   int = (5 * 1024 * 1024)

	metricBuildsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "bestie",
			Name:      "builds_total",
			Help:      "Total number of Bazel builds seen",
		},
	)
	metricEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "bestie",
			Name:      "events_total",
			Help:      "Total observed Bazel events, tagged by event ID",
		},
		[]string{"id"},
	)
)

type BuildEventService struct{}

func (s *BuildEventService) PublishLifecycleEvent(ctx context.Context, req *bpb.PublishLifecycleEventRequest) (*emptypb.Empty, error) {
	glog.V(2).Infof("# BEP LifecycleEvent message:\n%s", prototext.Format(req))
	return &emptypb.Empty{}, nil
}

func (s *BuildEventService) PublishBuildToolEventStream(stream bpb.PublishBuildEvent_PublishBuildToolEventStreamServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		glog.V(2).Infof("# BEP BuildToolEvent message:\n%s", prototext.Format(req))

		// Access protobuf message sections of interest.
		obe := req.GetOrderedBuildEvent()
		event := obe.GetEvent()
		streamId := obe.GetStreamId()
		// bazelEvent := event.GetBazelEvent()

		// See BuildEvent.Event in build_events.pb.go for list of event types supported.
		switch buildEvent := event.Event.(type) {
		case *bpb.BuildEvent_BazelEvent:
			var bazelBuildEvent bes.BuildEvent
			if err := ptypes.UnmarshalAny(buildEvent.BazelEvent, &bazelBuildEvent); err != nil {
				return err
			}
			bazelEventId := bazelBuildEvent.GetId()
			if ok := bazelEventId.GetBuildFinished(); ok != nil {
				metricBuildsTotal.Inc()
			}
			metricEventsTotal.WithLabelValues(getEventLabel(bazelEventId.Id)).Inc()
			if m := bazelBuildEvent.GetTestResult(); m != nil {
				if err := handleTestResultEvent(bazelBuildEvent, streamId); err != nil {
					glog.Errorf("Error handling Bazel event %T: %s", bazelEventId.Id, err)
					return err
				}
			}
		default:
			glog.V(2).Infof("Ignoring Bazel event type %T", buildEvent)
		}

		res := &bpb.PublishBuildToolEventStreamResponse{
			StreamId:       req.GetOrderedBuildEvent().StreamId,
			SequenceNumber: req.GetOrderedBuildEvent().SequenceNumber,
		}
		if err := stream.Send(res); err != nil {
			return err
		}
	}
	return nil
}

// Command line arguments.
var (
	argBaseUrl     = flag.String("base_url", "", "Base URL for accessing output artifacts in the build cluster (required)")
	argDataset     = flag.String("dataset", "", "BigQuery dataset name (required) -- staging, production")
	argMaxFileSize = flag.Int("max_file_size", maxFileSize, "Maximum output file size allowed for processing")
	argTableName   = flag.String("table_name", "testmetrics", "BigQuery table name")
	// gRPC max message size needs to match the max size of the sender (e.g.
	// BuildBuddy, Bazel). Bazel targets ~50MB messages, so that is the default
	// here.
	argMaxMessageSize = flag.Int("grpc_max_message_size_bytes", 50*1024*1024, "Maximum receive message size in bytes accepted by gRPC methods")
)

func checkCommandArgs() error {
	var errs []error
	// The --baseurl command line arg is required.
	// Note: This value is ignored for local invocations of the BES Endpoint and can be set to anything.
	if len(*argBaseUrl) == 0 {
		errs = append(errs, fmt.Errorf("--base_url must be specified"))
	}
	// The --dataset command line arg is required.
	if len(*argDataset) == 0 {
		errs = append(errs, fmt.Errorf("--dataset must be specified"))
	}
	if len(errs) > 0 {
		return multierror.New(errs)
	}

	// Set/override the default values.
	deploymentBaseUrl = *argBaseUrl
	maxFileSize = *argMaxFileSize
	bigQueryTableDefault.dataset = *argDataset
	bigQueryTableDefault.tableName = *argTableName

	return nil
}

func main() {
	ctx := context.Background()

	flag.Parse()
	if err := checkCommandArgs(); err != nil {
		glog.Exitf("Invalid command: %s", err)
	}

	grpcs := grpc.NewServer(
		grpc.MaxRecvMsgSize(*argMaxMessageSize),
	)
	bpb.RegisterPublishBuildEventServer(grpcs, &BuildEventService{})

	mux := http.NewServeMux()
	metrics.AddHandler(mux, "/metrics")

	glog.Exit(server.Run(ctx, mux, grpcs, nil))
}
