/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package inject

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Target identifies one database container without relying on a Service.
type Target struct {
	Pod         string
	Container   string
	ContainerID string
	PodIP       string
	Node        string
}

// PartitionSpec is the reviewed dedicated-chain input.
type PartitionSpec struct {
	Target         Target
	OracleIP       string
	ControlPlaneIP string
	PeerNodeIPs    []string
	PeerPodCIDRs   []string
	ServiceAPIIP   string
}

// Marker brackets host actions on the oracle clock and probes reachability
// from the oracle Pod.
type Marker interface {
	Mark(context.Context, string, string) error
	Probe(context.Context, string, []int) (map[int]string, error)
	ObserveContainer(context.Context, ContainerObservation) error
}

// ContainerObservation is host-assisted, read-only PID namespace evidence.
type ContainerObservation struct {
	Node        string `json:"node"`
	Source      string `json:"source"`
	OK          bool   `json:"ok"`
	PID1Comm    string `json:"pid1_comm,omitempty"`
	PID1Cmdline string `json:"pid1_cmdline,omitempty"`
	ZombieCount int    `json:"zombie_count,omitempty"`
	Cgroup      string `json:"cgroup,omitempty"`
	Raw         string `json:"raw,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Faults contains the only state-changing primitive constructors.
type Faults struct {
	Context   string
	Namespace string
	Commands  *Recorder
	Guard     ContextGuard
	Marker    Marker
	ScriptDir string
}

// SignalContainerInit sends KILL or TERM from the kind node's host PID
// namespace. The live context guard is the immediately preceding command.
func (f Faults) SignalContainerInit(ctx context.Context, target Target, signalName string) error {
	if signalName != "KILL" && signalName != "TERM" {
		return fmt.Errorf("signal %q is outside KILL/TERM allowlist", signalName)
	}
	label := "signal-" + strings.ToLower(signalName) + "-pid1-" + target.Pod
	return f.bracket(ctx, label, func() error {
		if err := f.Guard.Verify(ctx); err != nil {
			return err
		}
		return f.runNodeScript(ctx, target.Node, "signal-container-init.sh", target.ContainerID, signalName)
	})
}

// DemonstrateNamespacePID1Noop records that an in-namespace KILL to PID 1
// returns success without replacing the container namespace.
func (f Faults) DemonstrateNamespacePID1Noop(ctx context.Context, target Target) error {
	before, err := f.podContainerEvidence(ctx, target)
	if err != nil {
		return err
	}
	return f.bracket(ctx, "inside-namespace-pid1-noop-"+target.Pod, func() error {
		if err := f.Guard.Verify(ctx); err != nil {
			return err
		}
		result := f.Commands.Run(ctx, []string{"kubectl", "--context", f.Context, "-n", f.Namespace, "exec", target.Pod, "-c", target.Container, "--", "kill", "-9", "1"}, nil)
		if result.ExitCode != 0 {
			return fmt.Errorf("inside-namespace PID 1 demonstration exit %d: %s", result.ExitCode, result.Stderr)
		}
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		after, err := f.podContainerEvidence(ctx, target)
		if err != nil {
			return err
		}
		if before.ContainerID != after.ContainerID || before.RestartCount != after.RestartCount {
			return fmt.Errorf("inside-namespace PID 1 signal replaced container: before=%+v after=%+v", before, after)
		}
		return nil
	})
}

// OOMContainer lowers memory.max below current usage and restores it through
// the node-side script's EXIT trap.
func (f Faults) OOMContainer(ctx context.Context, target Target) error {
	return f.bracket(ctx, "cgroup-oom-"+target.Pod, func() error {
		if err := f.Guard.Verify(ctx); err != nil {
			return err
		}
		return f.runNodeScript(ctx, target.Node, "oom-container.sh", target.ContainerID)
	})
}

// PartitionNode installs the dedicated iptables chains after a fresh guard.
func (f Faults) PartitionNode(ctx context.Context, spec PartitionSpec) error {
	return f.bracket(ctx, "partition-"+spec.Target.Pod, func() error {
		if err := f.Guard.Verify(ctx); err != nil {
			return err
		}
		return f.runNodeScript(ctx, spec.Target.Node, "partition-node.sh", "apply", spec.OracleIP, spec.ControlPlaneIP, strings.Join(spec.PeerNodeIPs, ","), strings.Join(spec.PeerPodCIDRs, ","), spec.ServiceAPIIP)
	})
}

// HealNode removes both dedicated chains after a fresh guard.
func (f Faults) HealNode(ctx context.Context, target Target) error {
	return f.bracket(ctx, "heal-partition-"+target.Pod, func() error {
		if err := f.Guard.Verify(ctx); err != nil {
			return err
		}
		return f.runNodeScript(ctx, target.Node, "partition-node.sh", "heal")
	})
}

func (f Faults) runNodeScript(ctx context.Context, node, name string, args ...string) error {
	path := filepath.Join(f.ScriptDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read node-side script %q: %w", name, err)
	}
	argv := []string{"docker", "exec", "-i", node, "bash", "-s", "--"}
	argv = append(argv, args...)
	result := f.Commands.Run(ctx, argv, data)
	if result.ExitCode != 0 {
		return fmt.Errorf("node-side script %s exit %d: %s", name, result.ExitCode, result.Stderr)
	}
	return nil
}

func (f Faults) bracket(ctx context.Context, label string, action func() error) error {
	if f.Marker == nil {
		return fmt.Errorf("oracle marker client is nil")
	}
	if err := f.Marker.Mark(ctx, label, "before"); err != nil {
		return err
	}
	actionErr := action()
	markErr := f.Marker.Mark(ctx, label, "after")
	if actionErr != nil {
		return actionErr
	}
	return markErr
}

type containerEvidence struct {
	ContainerID  string
	RestartCount int32
}

// ContainerEvidence is the public restart and namespace identity view.
type ContainerEvidence struct {
	ContainerID  string `json:"container_id"`
	RestartCount int32  `json:"restart_count"`
}

// Evidence reads the target container's current identity.
func (f Faults) Evidence(ctx context.Context, target Target) (ContainerEvidence, error) {
	value, err := f.podContainerEvidence(ctx, target)
	return ContainerEvidence{ContainerID: value.ContainerID, RestartCount: value.RestartCount}, err
}

func (f Faults) podContainerEvidence(ctx context.Context, target Target) (containerEvidence, error) {
	result := f.Commands.Run(ctx, []string{"kubectl", "--context", f.Context, "-n", f.Namespace, "get", "pod", target.Pod, "-o", "json"}, nil)
	if result.ExitCode != 0 {
		return containerEvidence{}, fmt.Errorf("get Pod evidence exit %d: %s", result.ExitCode, result.Stderr)
	}
	var pod struct {
		Status struct {
			ContainerStatuses []struct {
				Name         string `json:"name"`
				ContainerID  string `json:"containerID"`
				RestartCount int32  `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &pod); err != nil {
		return containerEvidence{}, err
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == target.Container {
			return containerEvidence{ContainerID: status.ContainerID, RestartCount: status.RestartCount}, nil
		}
	}
	return containerEvidence{}, fmt.Errorf("container %q is absent from Pod %q status", target.Container, target.Pod)
}

// HTTPMarker talks only to the oracle's port-forwarded control port.
type HTTPMarker struct {
	BaseURL string
	Client  *http.Client
}

// Mark appends one fault bracket on the oracle clock.
func (m HTTPMarker) Mark(ctx context.Context, label, phase string) error {
	return m.post(ctx, "/mark", map[string]any{"label": label, "phase": phase}, nil)
}

// Probe asks the oracle Pod to test direct reachability.
func (m HTTPMarker) Probe(ctx context.Context, host string, ports []int) (map[int]string, error) {
	var response struct {
		Results map[int]string `json:"results"`
	}
	err := m.post(ctx, "/probe", map[string]any{"host": host, "ports": ports}, &response)
	return response.Results, err
}

// ObserveContainer forwards read-only introspection for oracle-clock stamping.
func (m HTTPMarker) ObserveContainer(ctx context.Context, observation ContainerObservation) error {
	return m.post(ctx, "/observe/container", observation, nil)
}

func (m HTTPMarker) post(ctx context.Context, path string, body any, destination any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.BaseURL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("oracle control %s returned HTTP %d", path, response.StatusCode)
	}
	if destination != nil {
		return json.NewDecoder(response.Body).Decode(destination)
	}
	return nil
}
