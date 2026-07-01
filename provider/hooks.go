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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cloudbase/garm-provider-incus/config"
)

type hookPhase string

const (
	hookPhaseCreate hookPhase = "create"
	hookPhaseStart  hookPhase = "start"
	hookPhaseDelete hookPhase = "delete"
)

// hookContext is passed to hooks as JSON on stdin and mirrored into env vars.
type hookContext struct {
	Name         string `json:"name"`
	ControllerID string `json:"controller_id"`
	PoolID       string `json:"pool_id"`
	OSType       string `json:"os_type"`
	OSArch       string `json:"os_arch"`
	InstanceType string `json:"instance_type"`
}

// effectiveHookTimeout returns the timeout clamped to (0, cap].
func effectiveHookTimeout(hook *config.Hook) time.Duration {
	t := time.Duration(hook.Timeout) * time.Second
	if t <= 0 || t > config.DefaultHookTimeout*time.Second {
		return config.DefaultHookTimeout * time.Second
	}
	return t
}

// runHook execs hook (nil/empty command = no-op returning stdin) and returns stdout.
func runHook(ctx context.Context, hook *config.Hook, hookName string, phase hookPhase, hc hookContext, stdin []byte) ([]byte, error) {
	if hook == nil || hook.Command == "" {
		return stdin, nil
	}

	timeout := effectiveHookTimeout(hook)
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", hook.Command)
	cmd.Env = append(os.Environ(),
		"GARM_HOOK="+hookName,
		"GARM_HOOK_PHASE="+string(phase),
		"GARM_INSTANCE_NAME="+hc.Name,
		"GARM_POOL_ID="+hc.PoolID,
		"GARM_CONTROLLER_ID="+hc.ControllerID,
		"GARM_OS_TYPE="+hc.OSType,
		"GARM_OS_ARCH="+hc.OSArch,
		"GARM_INSTANCE_TYPE="+hc.InstanceType,
		fmt.Sprintf("GARM_HOOK_IGNORE_FAILURE=%t", hook.IgnoreFailure),
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("hook %q timed out after %s: %s", hookName, timeout, stderr.String())
		}
		return nil, fmt.Errorf("hook %q failed: %w: %s", hookName, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// runSideEffectHook runs a hook (stdout ignored); errors only when ignore_failure is false.
func runSideEffectHook(ctx context.Context, hook *config.Hook, hookName string, phase hookPhase, hc hookContext) error {
	if hook == nil || hook.Command == "" {
		return nil
	}
	stdin, err := json.Marshal(hc)
	if err != nil {
		return fmt.Errorf("marshaling hook context: %w", err)
	}
	if _, err := runHook(ctx, hook, hookName, phase, hc, stdin); err != nil {
		if hook.IgnoreFailure {
			fmt.Fprintf(os.Stderr, "ignoring hook %q failure: %v\n", hookName, err)
			return nil
		}
		return err
	}
	return nil
}
