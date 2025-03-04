package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	fpb "github.com/System233/enkit/flextape/proto"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	metricRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: "flextape",
		Name:      "request_duration_seconds",
		Help:      "RPC execution time as seen by the server",
	},
		[]string{
			"method",
			"response_code",
		},
	)
	metricJanitorDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Subsystem: "flextape",
		Name:      "janitor_duration_seconds",
		Help:      "Janitor execution time",
	})
	metricRequestCodes = promauto.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "flextape",
		Name:      "response_count",
		Help:      "Total number of response codes sent",
	},
		[]string{
			"method",
			"response_code",
		},
	)
)

// Service implements the LicenseManager gRPC service.
type Service struct {
	mu           sync.Mutex          // Protects the following members from concurrent access
	currentState state               // State of the server
	licenses     map[string]*license // Queues and allocations, managed per-license-type

	queueRefreshDuration      time.Duration // Queue entries not refreshed within this duration are expired
	allocationRefreshDuration time.Duration // Allocations not refreshed within this duration are expired
}

func licensesFromConfig(config *fpb.Config) map[string]*license {
	licenses := map[string]*license{}
	for _, l := range config.GetLicenseConfigs() {
		name := fmt.Sprintf("%s::%s", l.GetLicense().GetVendor(), l.GetLicense().GetFeature())

		var prioritizer Prioritizer
		switch l.Prioritizer.(type) {
		case *fpb.LicenseConfig_Fifo:
			prioritizer = &FIFOPrioritizer{}
		case *fpb.LicenseConfig_EvenOwners:
			prioritizer = NewEvenOwnersPrioritizer()
		default:
			prioritizer = &FIFOPrioritizer{}
		}

		licenses[name] = &license{
			name:           name,
			totalAvailable: int(l.GetQuantity()),
			allocations:    map[string]*invocation{},
			prioritizer:    prioritizer,
		}
	}
	return licenses
}

func defaultUint32(v, d uint32) uint32 {
	if v == 0 {
		return d
	}
	return v
}

func New(config *fpb.Config) (*Service, error) {
	if config.GetServer() == nil {
		return nil, fmt.Errorf("missing `server` section in config")
	}
	queueRefreshSeconds := defaultUint32(config.GetServer().GetQueueRefreshDurationSeconds(), 15)
	allocationRefreshSeconds := defaultUint32(config.GetServer().GetAllocationRefreshDurationSeconds(), 30)
	janitorIntervalSeconds := defaultUint32(config.GetServer().GetJanitorIntervalSeconds(), 1)
	adoptionDurationSeconds := defaultUint32(config.GetServer().GetAdoptionDurationSeconds(), 45)

	licenses := licensesFromConfig(config)

	service := &Service{
		currentState:              stateStarting,
		licenses:                  licenses,
		queueRefreshDuration:      time.Duration(queueRefreshSeconds) * time.Second,
		allocationRefreshDuration: time.Duration(allocationRefreshSeconds) * time.Second,
	}

	go func(s *Service) {
		// TODO: Read this from flags
		t := time.NewTicker(time.Duration(janitorIntervalSeconds) * time.Second)
		defer t.Stop()
		for {
			<-t.C
			s.janitor()
		}
	}(service)

	go func(s *Service) {
		// TODO: Read this from flags
		<-time.After(time.Duration(adoptionDurationSeconds) * time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.currentState = stateRunning
	}(service)

	return service, nil
}

// QueueID is a monotonically increasing number representing the absolute
// position of the item in the queue from when the queue was last emptied.
//
// If the element is reordered, so to be dequeued earlier, its QueueID
// will be changed accordingly.
type QueueID uint64

// Position represents a relative position within the queue.
//
// For example, if this is the 3rd element in the queue, Position will be 3.
//
// Given the QueueID of an element its Position can be computed in O(1) by
// subtracting the QueueID of the first element currently in the queue.
type Position uint32

// invocation maps to a particular command invocation that has requested a
// license, and its associated metadata.
type invocation struct {
	ID          string    // Server-generated unique ID
	Owner       string    // Client-provided owner
	BuildTag    string    // Client-provided build tag. May not be unique across invocations
	LastCheckin time.Time // Time the invocation last had its queue position/allocation refreshed.

	QueueID QueueID // Position in the queue. 0 means the invocation has not been queued yet.
}

func (i *invocation) ToProto() *fpb.Invocation {
	return &fpb.Invocation{
		Owner:    i.Owner,
		BuildTag: i.BuildTag,
		Id:       i.ID,
	}
}

type state int

const (
	// Startup state during which server "adopts" unknown allocations. This is a
	// relatively short period (roughly 2x a refresh period) which helps
	// transition existing invocations in the event of a server restart without
	// unnecessarily cancelling invocations.
	stateStarting state = iota
	// Normal operating state.
	stateRunning
)

var (
	// generateRandomID returns a UUIDv4 string, and can be stubbed out for unit
	// tests.
	generateRandomID = func() (string, error) {
		id, err := uuid.NewRandom()
		if err != nil {
			return "", err
		}
		return id.String(), nil
	}

	// timeNow returns the current time, and can be stubbed out for unit tests.
	timeNow = time.Now
)

// janitor runs in a loop to cleanup allocations and queue spots that have not
// been refreshed in a sufficient amount of time, as well as to promote queued
// licenses to allocations.
func (s *Service) janitor() {
	defer updateJanitorMetrics(time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()
	// Don't expire or promote anything during startup.
	if s.currentState == stateStarting {
		return
	}
	allocationExpiry := timeNow().Add(-s.allocationRefreshDuration)
	queueExpiry := timeNow().Add(-s.queueRefreshDuration)
	for _, lic := range s.licenses {
		lic.ExpireAllocations(allocationExpiry)
		lic.ExpireQueued(queueExpiry)
		lic.Promote()
	}
}

func updateJanitorMetrics(startTime time.Time) {
	d := time.Now().Sub(startTime)
	metricJanitorDuration.Observe(d.Seconds())
}

func updateMetrics(method string, err *error, startTime time.Time) {
	d := time.Now().Sub(startTime)
	code := status.Code(*err)
	metricRequestCodes.WithLabelValues(method, code.String()).Inc()
	metricRequestDuration.WithLabelValues(method, code.String()).Observe(d.Seconds())
}

// Allocate allocates a license to the requesting invocation, or queues the
// request if none are available. See the proto docstrings for more details.
func (s *Service) Allocate(ctx context.Context, req *fpb.AllocateRequest) (retRes *fpb.AllocateResponse, retErr error) {
	defer updateMetrics("Allocate", &retErr, time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	invMsg := req.GetInvocation()
	if len(invMsg.GetLicenses()) != 1 {
		return nil, status.Errorf(codes.InvalidArgument, "licenses must have exactly one license spec")
	}
	licenseType := formatLicenseType(invMsg.GetLicenses()[0])
	lic, ok := s.licenses[licenseType]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown license type: %q", licenseType)
	}
	invocationID := invMsg.GetId()

	if invocationID == "" {
		// This is the first AllocationRequest by this invocation. Generate an ID
		// and queue it.
		var err error
		invocationID, err = generateRandomID()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate invocation_id: %v", err)
		}
		inv := &invocation{
			ID:          invocationID,
			Owner:       invMsg.GetOwner(),
			BuildTag:    invMsg.GetBuildTag(),
			LastCheckin: timeNow(),
		}
		lic.Enqueue(inv)

		if s.currentState == stateRunning {
			lic.Promote()
		}
	}

	// Invocation ID should now be known and either queued (due to the above
	// insert, or from a previous request) or allocated (promoted from the queue
	// by above, or asynchronously by the janitor).

	if inv := lic.GetAllocated(invocationID); inv != nil {
		// Invocation is allocated
		inv.LastCheckin = timeNow()
		return &fpb.AllocateResponse{
			ResponseType: &fpb.AllocateResponse_LicenseAllocated{
				LicenseAllocated: &fpb.LicenseAllocated{
					InvocationId:           invocationID,
					LicenseRefreshDeadline: timestamppb.New(timeNow().Add(s.allocationRefreshDuration)),
				},
			},
		}, nil
	}
	if inv, pos := lic.GetQueued(invocationID); inv != nil {
		// Invocation is queued
		inv.LastCheckin = timeNow()
		return &fpb.AllocateResponse{
			ResponseType: &fpb.AllocateResponse_Queued{
				Queued: &fpb.Queued{
					InvocationId:  invocationID,
					NextPollTime:  timestamppb.New(timeNow().Add(s.queueRefreshDuration)),
					QueuePosition: uint32(pos),
				},
			},
		}, nil
	}
	// Invocation is not allocated or queued
	if s.currentState == stateRunning {
		// This invocation is unknown (possibly expired)
		return nil, status.Errorf(codes.FailedPrecondition, "invocation_id not found: %q", invocationID)
	}
	// This invocation was previously queued before the server restart; add it
	// back to the queue.
	inv := &invocation{
		ID:          invocationID,
		Owner:       invMsg.GetOwner(),
		BuildTag:    invMsg.GetBuildTag(),
		LastCheckin: timeNow(),
	}
	pos := lic.Enqueue(inv)
	return &fpb.AllocateResponse{
		ResponseType: &fpb.AllocateResponse_Queued{
			Queued: &fpb.Queued{
				InvocationId:  invocationID,
				NextPollTime:  timestamppb.New(timeNow().Add(s.queueRefreshDuration)),
				QueuePosition: uint32(pos),
			},
		},
	}, nil
}

// Refresh serves as a keepalive to refresh an allocation while an invocation
// is still using it. See the proto docstrings for more info.
func (s *Service) Refresh(ctx context.Context, req *fpb.RefreshRequest) (retRes *fpb.RefreshResponse, retErr error) {
	defer updateMetrics("Refresh", &retErr, time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	invMsg := req.GetInvocation()
	if len(invMsg.GetLicenses()) != 1 {
		return nil, status.Errorf(codes.InvalidArgument, "licenses must have exactly one license spec")
	}
	licenseType := formatLicenseType(invMsg.GetLicenses()[0])
	lic, ok := s.licenses[licenseType]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown license type: %q", licenseType)
	}
	invID := invMsg.GetId()
	if invID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "invocation_id must be set")
	}
	inv := lic.GetAllocated(invID)
	if inv == nil {
		if s.currentState == stateRunning {
			return nil, status.Errorf(codes.FailedPrecondition, "invocation_id not allocated: %q", invID)
		}
		// "Adopt" this invocation and allocate it a license, if possible.
		inv := &invocation{
			ID:          invID,
			Owner:       invMsg.GetOwner(),
			BuildTag:    invMsg.GetBuildTag(),
			LastCheckin: timeNow(),
		}
		if ok := lic.Allocate(inv); ok {
			return &fpb.RefreshResponse{
				InvocationId:           invID,
				LicenseRefreshDeadline: timestamppb.New(timeNow().Add(s.allocationRefreshDuration)),
			}, nil
		} else {
			return nil, status.Errorf(codes.ResourceExhausted, "%q has no available licenses", licenseType)
		}
	}
	// Update the time and return the next check interval
	inv.LastCheckin = timeNow()
	return &fpb.RefreshResponse{
		InvocationId:           invID,
		LicenseRefreshDeadline: timestamppb.New(timeNow().Add(s.allocationRefreshDuration)),
	}, nil
}

// Release returns an allocated license and/or unqueues the specified
// invocation ID across all license types. See the proto docstrings for more
// details.
func (s *Service) Release(ctx context.Context, req *fpb.ReleaseRequest) (retRes *fpb.ReleaseResponse, retErr error) {
	defer updateMetrics("Release", &retErr, time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	invID := req.GetInvocationId()
	if invID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "invocation_id must be set")
	}
	count := 0
	for _, lic := range s.licenses {
		count += lic.Forget(invID)
	}
	if count == 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "invocation_id not found: %q", invID)
	}
	return &fpb.ReleaseResponse{}, nil
}

// LicensesStatus returns the status for every license type. See the proto
// docstrings for more details.
func (s *Service) LicensesStatus(ctx context.Context, req *fpb.LicensesStatusRequest) (retRes *fpb.LicensesStatusResponse, retErr error) {
	defer updateMetrics("LicensesStatus", &retErr, time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	res := &fpb.LicensesStatusResponse{}
	for _, lic := range s.licenses {
		res.LicenseStats = append(res.LicenseStats, lic.GetStats())
	}
	// Sort by vendor, then feature, with two groups: first group has either
	// allocations or queued invocations, second group has neither.
	sort.Slice(res.LicenseStats, func(i, j int) bool {
		aHasEntries := res.LicenseStats[i].GetAllocatedCount() > 0 || res.LicenseStats[i].GetQueuedCount() > 0
		bHasEntries := res.LicenseStats[j].GetAllocatedCount() > 0 || res.LicenseStats[j].GetQueuedCount() > 0
		if aHasEntries != bHasEntries {
			return aHasEntries
		}
		licA, licB := res.LicenseStats[i].GetLicense(), res.LicenseStats[j].GetLicense()
		if licA.GetVendor() == licB.GetVendor() {
			return licA.GetFeature() < licB.GetFeature()
		}
		return licA.GetVendor() < licB.GetVendor()
	})
	return res, nil
}
