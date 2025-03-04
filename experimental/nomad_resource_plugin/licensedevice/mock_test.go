package licensedevice

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/System233/enkit/experimental/nomad_resource_plugin/licensedevice/types"
)

type mockNotifier struct {
	mock.Mock
}

func (m *mockNotifier) GetCurrent(ctx context.Context) ([]*types.License, error) {
	args := m.Called()
	return args.Get(0).([]*types.License), args.Error(1)
}

func (m *mockNotifier) Chan(ctx context.Context) chan struct{} {
	args := m.Called()
	return args.Get(0).(chan struct{})
}

type mockReserver struct {
	mock.Mock
}

func (m *mockReserver) Reserve(ctx context.Context, licenseIDs []string, node string) ([]*types.License, error) {
	args := m.Called(ctx, licenseIDs, node)
	return args.Get(0).([]*types.License), args.Error(1)
}

func (m *mockReserver) UpdateInUse(ctx context.Context, licenses []*types.License) error {
	args := m.Called(ctx, licenses)
	return args.Error(0)
}
