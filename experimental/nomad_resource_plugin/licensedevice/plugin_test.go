package licensedevice

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/hashicorp/nomad/plugins/device"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/System233/enkit/experimental/nomad_resource_plugin/licensedevice/types"
	"github.com/System233/enkit/lib/str"
)

var sampleLicenseTable = []*types.License{
	{
		ID:      "aaaa",
		Vendor:  "vendor_a",
		Feature: "feature_1",
		Status:  "FREE",
	},
	{
		ID:          "bbbb",
		Vendor:      "vendor_b",
		Feature:     "feature_2",
		Status:      "IN_USE",
		UserNode:    str.Pointer("node-1234"),
		UserProcess: str.Pointer("job-abcd"),
	},
	{
		ID:          "cccc",
		Vendor:      "vendor_c",
		Feature:     "feature_3",
		Status:      "RESERVED",
		UserNode:    str.Pointer("node-2345"),
		UserProcess: str.Pointer("job-bcde"),
	},
}

func TestPluginIsNomadDevicePlugin(t *testing.T) {
	var pluginType *device.DevicePlugin
	assert.Implements(t, pluginType, &Plugin{})
}

func TestPluginFingerprintBeforeSetConfig(t *testing.T) {
	p := NewPlugin()
	_, gotErr := p.Fingerprint(context.Background())

	assert.Error(t, gotErr)
}

func TestPluginFingerprint(t *testing.T) {
	notifier := &mockNotifier{}

	p := NewPlugin()
	p.globalUpdater = notifier
	p.licenseHandleRoot = bazel.TestTmpDir()

	notifyChan := make(chan struct{})
	notifier.On("Chan").Return(notifyChan)
	notifier.On("GetCurrent").Return(sampleLicenseTable, nil)

	ctx, cancel := context.WithCancel(context.Background())
	gotChan, gotErr := p.Fingerprint(ctx)

	if !assert.NoError(t, gotErr) {
		return
	}

	for i := 0; i < 5; i++ {
		var got *device.FingerprintResponse
		// Drain the channel first to avoid test hang with make chan of 0 length
		go func() {
			got = <-gotChan
		}()
		// We automatically put up a fingerprint on initialization, so skip requesting a refresh once to account for it.
		if i != 0 {
			notifyChan <- struct{}{}
		}

		if !assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotNil(c, got)
		}, 1*time.Second, 10*time.Millisecond, "never got license info") {
			return
		}

		assert.Equal(t, &device.FingerprintResponse{
			Devices: []*device.DeviceGroup{
				{
					Type:   "flexlm_license",
					Vendor: "vendor_a",
					Name:   "feature_1",
					Devices: []*device.Device{
						{
							ID:      "aaaa",
							Healthy: true,
						},
					},
				},
				{
					Type:   "flexlm_license",
					Vendor: "vendor_b",
					Name:   "feature_2",
					Devices: []*device.Device{
						{
							ID:         "bbbb",
							Healthy:    false,
							HealthDesc: "in use by job job-abcd on node node-1234",
						},
					},
				},
				{
					Type:   "flexlm_license",
					Vendor: "vendor_c",
					Name:   "feature_3",
					Devices: []*device.Device{
						{
							ID:         "cccc",
							Healthy:    false,
							HealthDesc: "reserved by job job-bcde on node node-2345",
						},
					},
				},
			},
		}, got)
	}

	cancel()

	var got *device.FingerprintResponse
	go func() {
		got = <-gotChan
	}()
	if !assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotNil(c, got)
	}, 1*time.Second, 10*time.Millisecond, "never got channel response") {
		return
	}

	assert.Equal(t, &device.FingerprintResponse{Error: fmt.Errorf("context canceled")}, got)
}

func TestReserve(t *testing.T) {
	reserver := &mockReserver{}

	p := NewPlugin()
	p.nodeID = "client_a"
	p.reserver = reserver
	p.licenseHandleRoot = bazel.TestTmpDir()

	reserver.On("Reserve", mock.Anything, []string{"aaaa", "bbbb"}, "client_a").Return([]*types.License{
		{
			ID:          "aaaa",
			Vendor:      "vendor_a",
			Feature:     "feature_1",
			Status:      "RESERVED",
			UserNode:    str.Pointer("client_a"),
			UserProcess: nil,
		},
		{
			ID:          "bbbb",
			Vendor:      "vendor_b",
			Feature:     "feature_2",
			Status:      "RESERVED",
			UserNode:    str.Pointer("client_a"),
			UserProcess: nil,
		},
	}, nil)

	got, gotErr := p.Reserve([]string{"aaaa", "bbbb"})

	if !assert.NoError(t, gotErr) {
		return
	}

	assert.Equal(t, &device.ContainerReservation{
		Envs: map[string]string{
			"LICENSEPLUGIN_RESERVED_IDS": "aaaa,bbbb",
		},
	}, got)
}
