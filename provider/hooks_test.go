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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudbase/garm-provider-incus/config"
	"github.com/stretchr/testify/require"
)

// writeExecScript writes body to an executable /bin/sh script and returns its path.
func writeExecScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return path
}

func TestEffectiveHookTimeout(t *testing.T) {
	cases := map[int]time.Duration{
		0:   config.DefaultHookTimeout * time.Second,
		-5:  config.DefaultHookTimeout * time.Second,
		600: config.DefaultHookTimeout * time.Second,
		30:  30 * time.Second,
		60:  60 * time.Second,
	}
	for in, want := range cases {
		require.Equal(t, want, effectiveHookTimeout(&config.Hook{Timeout: in}), "timeout=%d", in)
	}
}

func TestRunHookStdinStdout(t *testing.T) {
	hook := &config.Hook{Command: writeExecScript(t, "cat")}
	out, err := runHook(context.Background(), hook, "vm_pre_create", hookPhaseCreate, hookContext{}, []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, "payload", string(out))
}

func TestRunHookEnv(t *testing.T) {
	hook := &config.Hook{Command: writeExecScript(t,
		`printf '%s|%s|%s|%s|%s|%s|%s|%s|%s' "$GARM_HOOK" "$GARM_HOOK_PHASE" "$GARM_INSTANCE_NAME" "$GARM_POOL_ID" "$GARM_CONTROLLER_ID" "$GARM_OS_TYPE" "$GARM_OS_ARCH" "$GARM_INSTANCE_TYPE" "$GARM_HOOK_IGNORE_FAILURE"`),
		IgnoreFailure: true,
	}
	hc := hookContext{Name: "inst", ControllerID: "ctrl", PoolID: "pool", OSType: "linux", OSArch: "amd64", InstanceType: "virtual-machine"}
	out, err := runHook(context.Background(), hook, "vm_post_start", hookPhaseStart, hc, nil)
	require.NoError(t, err)
	require.Equal(t, "vm_post_start|start|inst|pool|ctrl|linux|amd64|virtual-machine|true", string(out))
}

func TestRunHookNonZeroExit(t *testing.T) {
	hook := &config.Hook{Command: writeExecScript(t, "echo boom >&2; exit 1")}
	_, err := runHook(context.Background(), hook, "vm_pre_start", hookPhaseStart, hookContext{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestRunHookTimeout(t *testing.T) {
	hook := &config.Hook{Command: writeExecScript(t, "sleep 3"), Timeout: 1}
	_, err := runHook(context.Background(), hook, "vm_post_start", hookPhaseStart, hookContext{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

func TestRunHookNilOrEmpty(t *testing.T) {
	out, err := runHook(context.Background(), nil, "vm_pre_create", hookPhaseCreate, hookContext{}, []byte("keep"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(out))

	out, err = runHook(context.Background(), &config.Hook{}, "vm_pre_create", hookPhaseCreate, hookContext{}, []byte("keep"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(out))
}

func TestRunSideEffectHookIgnoreFailure(t *testing.T) {
	fail := writeExecScript(t, "exit 1")

	require.NoError(t, runSideEffectHook(context.Background(),
		&config.Hook{Command: fail, IgnoreFailure: true}, "vm_pre_delete", hookPhaseDelete, hookContext{}))

	require.Error(t, runSideEffectHook(context.Background(),
		&config.Hook{Command: fail, IgnoreFailure: false}, "vm_post_create", hookPhaseCreate, hookContext{}))

	require.NoError(t, runSideEffectHook(context.Background(), nil, "vm_post_create", hookPhaseCreate, hookContext{}))
}
