// SPDX-License-Identifier: Apache-2.0
// Copyright 2023 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm-provider-incus/config"
	"github.com/lxc/incus/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetCLI(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Incus{
		UnixSocket: "/var/run/incus.sock",
	}
	l := &Incus{
		cfg: cfg,
		cli: &MockIncusServer{},
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}

	_, err := l.getCLI(ctx)
	require.NoError(t, err)
}

func TestGetProfiles(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)

	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	expected := []string{"default", "project"}

	cli.On("GetProfileNames").Return(expected, nil)
	profiles, err := l.getProfiles(ctx, "project")
	require.NoError(t, err)
	require.Equal(t, expected, profiles)
}

func TestGetCreateInstanceArgsContainer(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)

	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "container",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	tools := []commonParams.RunnerApplicationDownload{
		{
			OS:           ptr("ubuntu"),
			Architecture: ptr("x86_64"),
			DownloadURL:  ptr("https://example.com"),
			Filename:     ptr("test-app"),
		},
	}
	aliases := map[string]*api.ImageAliasesEntry{
		"x86_64": {
			Name: "ubuntu",
			Type: "container",
		},
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}

	cli.On("GetImageAliasArchitectures", config.IncusImageType("container").String(), "ubuntu").Return(aliases, nil)
	cli.On("GetImage", aliases["x86_64"].Target).Return(&api.Image{Fingerprint: "123abc"}, "", nil)
	cli.On("GetProfileNames").Return([]string{"default", "container"}, nil)
	specs := extraSpecs{}
	tests := []struct {
		name            string
		bootstrapParams commonParams.BootstrapInstance
		expected        api.InstancesPost
		errString       string
	}{
		{
			name:            "missing name",
			bootstrapParams: commonParams.BootstrapInstance{},
			expected:        api.InstancesPost{},
			errString:       "missing name",
		},
		{
			name: "looking for profile fails",
			bootstrapParams: commonParams.BootstrapInstance{
				Name:    "test-instance",
				Tools:   tools,
				Image:   "ubuntu",
				Flavor:  "bad-flavor",
				RepoURL: "mock-repo-url",
				PoolID:  "default",
				OSArch:  commonParams.Amd64,
				OSType:  commonParams.Linux,
			},
			expected:  api.InstancesPost{},
			errString: "looking for profile",
		},
		{
			name: "bad architecture fails",
			bootstrapParams: commonParams.BootstrapInstance{
				Name:    "test-instance",
				Tools:   tools,
				Image:   "ubuntu",
				Flavor:  "container",
				RepoURL: "mock-repo-url",
				PoolID:  "default",
				OSArch:  "bad-arch",
				OSType:  commonParams.Linux,
			},
			expected:  api.InstancesPost{},
			errString: "architecture bad-arch is not supported",
		},
		{
			name: "success container instance",
			bootstrapParams: commonParams.BootstrapInstance{
				Name:    "test-instance",
				Tools:   tools,
				Image:   "ubuntu",
				Flavor:  "container",
				RepoURL: "mock-repo-url",
				PoolID:  "default",
				OSArch:  commonParams.Amd64,
				OSType:  commonParams.Linux,
			},
			expected: api.InstancesPost{
				Name: "test-instance",
				InstancePut: api.InstancePut{
					Architecture: "x86_64",
					Profiles:     []string{"default", "container"},
					Description:  "Github runner provisioned by garm",
					Config: map[string]string{
						"user.user-data":    `#cloud-config`,
						osTypeKeyName:       "linux",
						osArchKeyNAme:       "amd64",
						controllerIDKeyName: "controller",
						poolIDKey:           "default",
					},
				},
				Source: api.InstanceSource{
					Type:        "image",
					Fingerprint: "123abc",
				},
				Type: "container",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ret, err := l.getCreateInstanceArgs(ctx, tt.bootstrapParams, specs)
			if tt.errString != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errString)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, ret)
		})
	}
}

func TestGetCreateInstanceArgsVM(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)

	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	tools := []commonParams.RunnerApplicationDownload{
		{
			OS:           ptr("windows"),
			Architecture: ptr("x86_64"),
			DownloadURL:  ptr("https://example.com"),
			Filename:     ptr("test-app"),
		},
	}
	aliases := map[string]*api.ImageAliasesEntry{
		"x86_64": {
			Name: "windows",
			Type: "container",
		},
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}

	cli.On("GetImageAliasArchitectures", config.IncusImageType("virtual-machine").String(), "windows").Return(aliases, nil)
	cli.On("GetImage", aliases["x86_64"].Target).Return(&api.Image{Fingerprint: "123abc"}, "", nil)
	cli.On("GetProfileNames").Return([]string{"default", "virtual-machine"}, nil)
	specs := extraSpecs{}
	tests := []struct {
		name            string
		bootstrapParams commonParams.BootstrapInstance
		expected        api.InstancesPost
		errString       string
	}{
		{
			name:            "missing name",
			bootstrapParams: commonParams.BootstrapInstance{},
			expected:        api.InstancesPost{},
			errString:       "missing name",
		},
		{
			name: "success vm instance",
			bootstrapParams: commonParams.BootstrapInstance{
				Name:    "test-instance",
				Tools:   tools,
				Image:   "windows",
				Flavor:  "virtual-machine",
				RepoURL: "mock-repo-url",
				PoolID:  "default",
				OSArch:  commonParams.Amd64,
				OSType:  commonParams.Windows,
			},
			expected: api.InstancesPost{
				Name: "test-instance",
				InstancePut: api.InstancePut{
					Architecture: "x86_64",
					Profiles:     []string{"default", "virtual-machine"},
					Description:  "Github runner provisioned by garm",
					Config: map[string]string{
						"user.user-data":      "#ps1_sysnative\n" + "#cloud-config",
						osTypeKeyName:         "windows",
						osArchKeyNAme:         "amd64",
						controllerIDKeyName:   "controller",
						poolIDKey:             "default",
						"security.secureboot": "false",
					},
				},
				Source: api.InstanceSource{
					Type:        "image",
					Fingerprint: "123abc",
				},
				Type: "virtual-machine",
			},
			errString: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ret, err := l.getCreateInstanceArgs(ctx, tt.bootstrapParams, specs)
			if tt.errString != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errString)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, ret)
		})
	}
}

func TestLaunchInstance(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)

	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	createArgs := api.InstancesPost{
		Name: "test-instance",
		InstancePut: api.InstancePut{
			Architecture: "x86_64",
			Profiles:     []string{"default", "container"},
			Description:  "Github runner provisioned by garm",
			Config: map[string]string{
				"user.user-data":    `#cloud-config`,
				osTypeKeyName:       "linux",
				osArchKeyNAme:       "amd64",
				controllerIDKeyName: "controller",
				poolIDKey:           "default",
			},
		},
		Source: api.InstanceSource{
			Type:        "image",
			Fingerprint: "123abc",
		},
		Type: "container",
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}
	mockOp := new(MockOperation)
	mockOp.On("Wait").Return(nil)
	cli.On("CreateInstance", createArgs).Return(mockOp, nil)
	cli.On("UpdateInstanceState", "test-instance", api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "").Return(mockOp, nil)

	err := l.launchInstance(ctx, createArgs, hookContext{}, config.Hooks{})
	require.NoError(t, err)
}

func TestCreateInstance(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	boostrapParams := commonParams.BootstrapInstance{
		Name: "test-instance",
		Tools: []commonParams.RunnerApplicationDownload{
			{
				OS:           ptr("windows"),
				Architecture: ptr("x86_64"),
				DownloadURL:  ptr("https://example.com"),
				Filename:     ptr("test-app"),
			},
		},
		Image:   "windows",
		Flavor:  "virtual-machine",
		RepoURL: "mock-repo-url",
		PoolID:  "default",
		OSArch:  commonParams.Amd64,
		OSType:  commonParams.Windows,
	}
	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	aliases := map[string]*api.ImageAliasesEntry{
		"x86_64": {
			Name: "windows",
			Type: "virtual-machine",
		},
	}
	expectedOutput := commonParams.ProviderInstance{
		OSArch:     commonParams.Amd64,
		ProviderID: "test-instance",
		Name:       "test-instance",
		OSType:     commonParams.Windows,
		OSName:     "windows",
		OSVersion:  "",
		Addresses: []commonParams.Address{
			{
				Address: "10.10.0.0",
				Type:    commonParams.PublicAddress,
			},
		},
		Status: commonParams.InstanceRunning,
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}
	cli.On("GetImageAliasArchitectures", config.IncusImageType("virtual-machine").String(), "windows").Return(aliases, nil)
	cli.On("GetImage", aliases["x86_64"].Target).Return(&api.Image{Fingerprint: "123abc"}, "", nil)
	cli.On("GetProfileNames").Return([]string{"default", "virtual-machine"}, nil)
	mockOp := new(MockOperation)
	mockOp.On("Wait").Return(nil)
	cli.On("CreateInstance", mock.Anything).Return(mockOp, nil)
	cli.On("UpdateInstanceState", "test-instance", api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "").Return(mockOp, nil)
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{
		Instance: api.Instance{
			InstancePut: api.InstancePut{
				Architecture: "x86_64",
			},
			Name: "test-instance",
			ExpandedConfig: map[string]string{
				"image.os":      "windows",
				"image.release": "",
			},
			Type: "container",
		},
		State: &api.InstanceState{
			Status: "Running",
			Network: map[string]api.InstanceStateNetwork{
				"eth0": {
					Addresses: []api.InstanceStateNetworkAddress{
						{
							Address: "10.10.0.0",
							Scope:   "global",
						},
					},
				},
			},
		},
	}, "", nil)

	ret, err := l.CreateInstance(ctx, boostrapParams)
	require.NoError(t, err)
	assert.Equal(t, expectedOutput, ret)
}

func TestGetInstance(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	instanceName := "test-instance"
	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{
		Instance: api.Instance{
			InstancePut: api.InstancePut{
				Architecture: "x86_64",
			},
			Name: "test-instance",
			ExpandedConfig: map[string]string{
				"image.os":      "windows",
				"image.release": "",
			},
			Type: "container",
		},
		State: &api.InstanceState{
			Status: "Running",
			Network: map[string]api.InstanceStateNetwork{
				"eth0": {
					Addresses: []api.InstanceStateNetworkAddress{
						{
							Address: "10.10.0.0",
							Scope:   "global",
						},
					},
				},
			},
		},
	}, "", nil)
	expectedOutput := commonParams.ProviderInstance{
		OSArch:     commonParams.Amd64,
		ProviderID: "test-instance",
		Name:       "test-instance",
		OSType:     commonParams.Windows,
		OSName:     "windows",
		OSVersion:  "",
		Addresses: []commonParams.Address{
			{
				Address: "10.10.0.0",
				Type:    commonParams.PublicAddress,
			},
		},
		Status: commonParams.InstanceRunning,
	}

	ret, err := l.GetInstance(ctx, instanceName)
	require.NoError(t, err)
	assert.Equal(t, expectedOutput, ret)
}

func TestDeleteInstance(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	instanceName := "test-instance"
	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	mockOp := new(MockOperation)
	mockOp.On("WaitContext", mock.Anything).Return(nil)
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{}, "", nil)
	cli.On("DeleteInstance", "test-instance").Return(mockOp, nil)
	cli.On("UpdateInstanceState", "test-instance", api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   true,
	}, "").Return(mockOp, nil)

	err := l.DeleteInstance(ctx, instanceName)
	require.NoError(t, err)
}

func TestListInstances(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	poolID := "test-pool-id"
	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}
	cli.On("GetInstancesFull", api.InstanceTypeAny).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				InstancePut: api.InstancePut{
					Architecture: "x86_64",
				},
				Name: "test-instance",
				ExpandedConfig: map[string]string{
					"image.os":          "windows",
					"image.release":     "",
					poolIDKey:           poolID,
					controllerIDKeyName: "controller",
				},
				Type: "container",
			},
			State: &api.InstanceState{
				Status: "Running",
				Network: map[string]api.InstanceStateNetwork{
					"eth0": {
						Addresses: []api.InstanceStateNetworkAddress{
							{
								Address: "10.10.0.0",
								Scope:   "global",
							},
						},
					},
				},
			},
		},
	}, nil)
	expectedOutput := []commonParams.ProviderInstance{
		{
			OSArch:     commonParams.Amd64,
			ProviderID: "test-instance",
			Name:       "test-instance",
			OSType:     commonParams.Windows,
			OSName:     "windows",
			OSVersion:  "",
			Addresses: []commonParams.Address{
				{
					Address: "10.10.0.0",
					Type:    commonParams.PublicAddress,
				},
			},
			Status: commonParams.InstanceRunning,
		},
	}

	ret, err := l.ListInstances(ctx, poolID)
	require.NoError(t, err)
	assert.Equal(t, expectedOutput, ret)
}

func TestRemoveAllInstances(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	poolID := "test-pool-id"
	instanceName := "test-instance"
	cfg := &config.Incus{
		UnixSocket:            "/var/run/incus.sock",
		InstanceType:          "virtual-machine",
		IncludeDefaultProfile: true,
	}
	l := &Incus{
		cfg: cfg,
		cli: cli,
		imageManager: &image{
			remotes: map[string]config.IncusImageRemote{
				"remote1": {
					Address: "remote1",
				},
			},
		},
		controllerID: "controller",
	}
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}
	cli.On("GetInstancesFull", api.InstanceTypeAny).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				InstancePut: api.InstancePut{
					Architecture: "x86_64",
				},
				Name: instanceName,
				ExpandedConfig: map[string]string{
					"image.os":          "windows",
					"image.release":     "",
					poolIDKey:           poolID,
					controllerIDKeyName: "controller",
				},
				Type: "container",
			},
			State: &api.InstanceState{
				Status: "Running",
			},
		},
	}, nil)
	mockOp := new(MockOperation)
	mockOp.On("WaitContext", mock.Anything).Return(nil)
	cli.On("GetInstanceFull", instanceName).Return(&api.InstanceFull{}, "", nil)
	cli.On("DeleteInstance", instanceName).Return(mockOp, nil)
	cli.On("UpdateInstanceState", "test-instance", api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   true,
	}, "").Return(mockOp, nil)

	err := l.RemoveAllInstances(ctx)
	require.NoError(t, err)
}

func TestStop(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	force := true
	instanceName := "test-instance"
	l := &Incus{
		cfg:          &config.Incus{},
		cli:          cli,
		imageManager: &image{},
		controllerID: "controller",
	}
	mockOp := new(MockOperation)
	mockOp.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", instanceName, api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   force,
	}, "").Return(mockOp, nil)
	err := l.Stop(ctx, instanceName, force)
	require.NoError(t, err)
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	instanceName := "test-instance"
	l := &Incus{
		cfg:          &config.Incus{},
		cli:          cli,
		imageManager: &image{},
		controllerID: "controller",
	}
	mockOp := new(MockOperation)
	mockOp.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", instanceName, api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
		Force:   false,
	}, "").Return(mockOp, nil)
	err := l.Start(ctx, instanceName)
	require.NoError(t, err)
}

// --- lifecycle hooks --------------------------------------------------------

func newHookProvider(cli InstanceServerInterface, hooks config.Hooks) *Incus {
	return &Incus{
		cfg: &config.Incus{
			UnixSocket:            "/var/run/incus.sock",
			InstanceType:          "virtual-machine",
			IncludeDefaultProfile: true,
			Hooks:                 hooks,
		},
		cli:          cli,
		imageManager: &image{remotes: map[string]config.IncusImageRemote{"remote1": {Address: "remote1"}}},
		controllerID: "controller",
	}
}

func hookBootstrap() commonParams.BootstrapInstance {
	return commonParams.BootstrapInstance{
		Name:   "test-instance",
		Tools:  []commonParams.RunnerApplicationDownload{{OS: ptr("linux"), Architecture: ptr("x86_64"), DownloadURL: ptr("https://example.com"), Filename: ptr("test-app")}},
		Image:  "linux",
		Flavor: "virtual-machine",
		PoolID: "default",
		OSArch: commonParams.Amd64,
		OSType: commonParams.Linux,
	}
}

// setupCreateFlow stubs the calls getCreateInstanceArgs+launchInstance make and
// returns a shared operation. CreateInstance/UpdateInstanceState are left to the
// caller so per-test capture/matchers are possible.
func setupCreateFlow(cli *MockIncusServer) *MockOperation {
	DefaultToolFetch = func(_ commonParams.OSType, _ commonParams.OSArch, tools []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return tools[0], nil
	}
	DefaultGetCloudconfig = func(_ commonParams.BootstrapInstance, _ commonParams.RunnerApplicationDownload, _ string) (string, error) {
		return "#cloud-config", nil
	}
	aliases := map[string]*api.ImageAliasesEntry{"x86_64": {Name: "linux", Type: "virtual-machine"}}
	cli.On("GetImageAliasArchitectures", config.IncusImageType("virtual-machine").String(), "linux").Return(aliases, nil)
	cli.On("GetImage", aliases["x86_64"].Target).Return(&api.Image{Fingerprint: "123abc"}, "", nil)
	cli.On("GetProfileNames").Return([]string{"default", "virtual-machine"}, nil)
	op := new(MockOperation)
	op.On("Wait").Return(nil)
	op.On("WaitContext", mock.Anything).Return(nil)
	return op
}

func mockInstanceWithIP(cli *MockIncusServer) {
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{
		Instance: api.Instance{
			InstancePut:    api.InstancePut{Architecture: "x86_64"},
			Name:           "test-instance",
			ExpandedConfig: map[string]string{"image.os": "ubuntu", poolIDKey: "default", osTypeKeyName: "linux"},
			Type:           "virtual-machine",
		},
		State: &api.InstanceState{
			Status:  "Running",
			Network: map[string]api.InstanceStateNetwork{"eth0": {Addresses: []api.InstanceStateNetworkAddress{{Address: "10.10.0.0", Scope: "global"}}}},
		},
	}, "", nil)
}

// markerHook returns a hook that appends $GARM_HOOK to the marker file and
// writes nothing to stdout.
func markerHook(marker string) *config.Hook {
	return &config.Hook{Command: fmt.Sprintf(`printf '%%s ' "$GARM_HOOK" >> %q`, marker)}
}

func TestCreateInstanceRunsHooksInOrder(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "order")
	l := newHookProvider(cli, config.Hooks{
		VMPreCreate:  markerHook(marker),
		VMPostCreate: markerHook(marker),
		VMPreStart:   markerHook(marker),
		VMPostStart:  markerHook(marker),
	})
	op := setupCreateFlow(cli)
	cli.On("CreateInstance", mock.Anything).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	_, err := l.CreateInstance(ctx, hookBootstrap())
	require.NoError(t, err)

	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "vm_pre_create vm_post_create vm_pre_start vm_post_start ", string(got))
}

func TestCreateInstancePreCreateMutation(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	jsonPath := filepath.Join(t.TempDir(), "out.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"name":"test-instance","architecture":"x86_64","description":"mutated-by-hook","type":"virtual-machine","source":{"type":"image","fingerprint":"123abc"},"profiles":["default","virtual-machine"],"config":{"user.runner-pool-id":"default"}}`), 0o644))

	l := newHookProvider(cli, config.Hooks{VMPreCreate: &config.Hook{Command: fmt.Sprintf("cat >/dev/null; cat %q", jsonPath)}})
	op := setupCreateFlow(cli)
	var got api.InstancesPost
	cli.On("CreateInstance", mock.Anything).Run(func(a mock.Arguments) { got = a.Get(0).(api.InstancesPost) }).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	_, err := l.CreateInstance(ctx, hookBootstrap())
	require.NoError(t, err)
	require.Equal(t, "mutated-by-hook", got.Description)
}

func TestCreateInstancePreCreateNameChangeRejected(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	jsonPath := filepath.Join(t.TempDir(), "out.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"name":"renamed","architecture":"x86_64","type":"virtual-machine","source":{"type":"image","fingerprint":"123abc"},"profiles":["default","virtual-machine"]}`), 0o644))
	l := newHookProvider(cli, config.Hooks{VMPreCreate: &config.Hook{Command: fmt.Sprintf("cat >/dev/null; cat %q", jsonPath)}})
	setupCreateFlow(cli)

	_, err := l.CreateInstance(ctx, hookBootstrap())
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not change the instance name")
	cli.AssertNotCalled(t, "CreateInstance", mock.Anything)
}

func TestCreateInstanceHookFailureCleansUp(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	l := newHookProvider(cli, config.Hooks{
		VMPostCreate: &config.Hook{Command: "exit 1"},
	})
	op := setupCreateFlow(cli)
	cli.On("CreateInstance", mock.Anything).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	_, err := l.CreateInstance(ctx, hookBootstrap())
	require.Error(t, err)
	cli.AssertCalled(t, "DeleteInstance", "test-instance")
}

func TestCreateInstanceHookIgnoreFailure(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	l := newHookProvider(cli, config.Hooks{
		VMPostCreate: &config.Hook{Command: "exit 1", IgnoreFailure: true},
	})
	op := setupCreateFlow(cli)
	cli.On("CreateInstance", mock.Anything).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	_, err := l.CreateInstance(ctx, hookBootstrap())
	require.NoError(t, err)
	cli.AssertNotCalled(t, "DeleteInstance", "test-instance")
}

func TestDeleteInstanceRunsDeleteHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	l := newHookProvider(cli, config.Hooks{
		VMPreDelete:  markerHook(marker),
		VMPostDelete: markerHook(marker),
	})
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	mockInstanceWithIP(cli)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "vm_pre_delete vm_post_delete ", string(got))
}

func TestDeleteInstanceDeleteHookFailureNonBlocking(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	l := newHookProvider(cli, config.Hooks{
		VMPreDelete: &config.Hook{Command: "exit 1"},
	})
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	mockInstanceWithIP(cli)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	cli.AssertCalled(t, "DeleteInstance", "test-instance")
}

func TestDeleteInstanceNoHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	l := newHookProvider(cli, config.Hooks{})
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{}, "", nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	cli.AssertCalled(t, "DeleteInstance", "test-instance")
}

func TestRemoveAllInstancesRunsDeleteHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	l := newHookProvider(cli, config.Hooks{VMPreDelete: markerHook(marker)})
	cli.On("GetInstancesFull", api.InstanceTypeAny).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				InstancePut:    api.InstancePut{Architecture: "x86_64"},
				Name:           "test-instance",
				ExpandedConfig: map[string]string{"image.os": "ubuntu", poolIDKey: "default", controllerIDKeyName: "controller"},
				Type:           "virtual-machine",
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	mockInstanceWithIP(cli)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.RemoveAllInstances(ctx))
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "vm_pre_delete ", string(got))
}

func hookBootstrapWithSpecs(specs string) commonParams.BootstrapInstance {
	bp := hookBootstrap()
	bp.ExtraSpecs = []byte(specs)
	return bp
}

func TestCreateInstanceExtraSpecsOverridesConfigHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	dir := t.TempDir()
	cfgMarker := filepath.Join(dir, "cfg")
	esMarker := filepath.Join(dir, "es")
	l := newHookProvider(cli, config.Hooks{
		VMPreCreate: &config.Hook{Command: fmt.Sprintf("printf config > %q", cfgMarker)},
	})
	op := setupCreateFlow(cli)
	cli.On("CreateInstance", mock.Anything).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	specs := fmt.Sprintf(`{"hooks":{"vm_pre_create":{"command":%q}}}`, fmt.Sprintf("printf extra > %q", esMarker))
	_, err := l.CreateInstance(ctx, hookBootstrapWithSpecs(specs))
	require.NoError(t, err)

	got, err := os.ReadFile(esMarker)
	require.NoError(t, err)
	require.Equal(t, "extra", string(got))
	require.NoFileExists(t, cfgMarker)
}

func TestCreateInstanceExtraSpecsEmptyDisablesConfigHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	cfgMarker := filepath.Join(t.TempDir(), "cfg")
	l := newHookProvider(cli, config.Hooks{
		VMPreCreate: &config.Hook{Command: fmt.Sprintf("printf config > %q", cfgMarker)},
	})
	op := setupCreateFlow(cli)
	cli.On("CreateInstance", mock.Anything).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	_, err := l.CreateInstance(ctx, hookBootstrapWithSpecs(`{"hooks":{}}`))
	require.NoError(t, err)
	require.NoFileExists(t, cfgMarker)
}

func TestCreateInstancePersistsDeleteHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	l := newHookProvider(cli, config.Hooks{})
	op := setupCreateFlow(cli)
	var got api.InstancesPost
	cli.On("CreateInstance", mock.Anything).Run(func(a mock.Arguments) { got = a.Get(0).(api.InstancesPost) }).Return(op, nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	mockInstanceWithIP(cli)

	_, err := l.CreateInstance(ctx, hookBootstrapWithSpecs(`{"hooks":{"vm_pre_delete":{"command":"true"}}}`))
	require.NoError(t, err)
	require.Contains(t, got.Config[deleteHooksKeyName], `"vm_pre_delete"`)
	require.Contains(t, got.Config[deleteHooksKeyName], `"command":"true"`)
}

func TestDeleteInstanceUsesPersistedHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	// config defines a failing delete hook; the persisted one must win.
	l := newHookProvider(cli, config.Hooks{VMPreDelete: &config.Hook{Command: "exit 1"}})
	persisted := fmt.Sprintf(`{"vm_pre_delete":{"command":%q}}`, fmt.Sprintf("printf persisted > %q", marker))
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{
		Instance: api.Instance{
			Name:           "test-instance",
			ExpandedConfig: map[string]string{deleteHooksKeyName: persisted},
		},
	}, "", nil)
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "persisted", string(got))
}

func TestDeleteInstanceCorruptPersistedHooksSkips(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	// A corrupt persisted value is replace-mode, so config hooks must NOT leak.
	l := newHookProvider(cli, config.Hooks{VMPreDelete: markerHook(marker)})
	cli.On("GetInstanceFull", "test-instance").Return(&api.InstanceFull{
		Instance: api.Instance{
			Name:           "test-instance",
			ExpandedConfig: map[string]string{deleteHooksKeyName: "not json{"},
		},
	}, "", nil)
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	require.NoFileExists(t, marker)
}

func TestDeleteInstanceNotFoundRunsNoHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	l := newHookProvider(cli, config.Hooks{VMPreDelete: markerHook(marker)})
	cli.On("GetInstanceFull", "test-instance").Return((*api.InstanceFull)(nil), "", os.ErrNotExist)
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	require.NoFileExists(t, marker)
}

func TestDeleteInstanceClientErrorUsesConfigHooks(t *testing.T) {
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "del")
	// cli is nil and the config has no endpoint, so getCLI fails; config hooks
	// must still run best-effort (the raw delete then errors).
	l := &Incus{
		cfg:          &config.Incus{Hooks: config.Hooks{VMPreDelete: markerHook(marker)}},
		imageManager: &image{},
		controllerID: "controller",
	}
	require.Error(t, l.DeleteInstance(ctx, "test-instance"))
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "vm_pre_delete ", string(got))
}

func TestDeleteInstanceReadErrorUsesConfigHooks(t *testing.T) {
	ctx := context.Background()
	cli := new(MockIncusServer)
	marker := filepath.Join(t.TempDir(), "del")
	l := newHookProvider(cli, config.Hooks{VMPreDelete: markerHook(marker)})
	// A non-not-found read error must not silently skip the delete hooks.
	cli.On("GetInstanceFull", "test-instance").Return((*api.InstanceFull)(nil), "", fmt.Errorf("boom"))
	op := new(MockOperation)
	op.On("WaitContext", mock.Anything).Return(nil)
	cli.On("UpdateInstanceState", "test-instance", mock.Anything, "").Return(op, nil)
	cli.On("DeleteInstance", "test-instance").Return(op, nil)

	require.NoError(t, l.DeleteInstance(ctx, "test-instance"))
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "vm_pre_delete ", string(got))
}
