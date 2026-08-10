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

package scenario

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/chaos/internal/inject"
)

// Driver owns bounded scenario orchestration. Every mutation is delegated to
// inject.Faults, whose methods re-run the context guard immediately before it.
type Driver struct {
	Context           string
	Namespace         string
	Name              string
	PartitionDuration time.Duration
	BundleDir         string
	Commands          *inject.Recorder
	Faults            inject.Faults
	Marker            inject.Marker
}

// Run executes one allowlisted scenario.
func (d Driver) Run(ctx context.Context, cluster inject.ClusterView) error {
	switch d.Name {
	case "s1-primary-crash":
		return d.runS1(ctx, cluster)
	case "s2-sigkill-oom":
		return d.runS2(ctx, cluster)
	case "s3-partition":
		return d.runS3(ctx, cluster)
	default:
		return fmt.Errorf("scenario %q is not allowlisted", d.Name)
	}
}

func (d Driver) runS1(ctx context.Context, cluster inject.ClusterView) error {
	before, err := d.Faults.Evidence(ctx, cluster.Leader)
	if err != nil {
		return err
	}
	if err := d.Faults.SignalContainerInit(ctx, cluster.Leader, "KILL"); err != nil {
		return err
	}
	if _, err := d.waitForRestart(ctx, cluster.Leader, before, 2*time.Minute); err != nil {
		return err
	}
	return d.capturePreviousLog(ctx, cluster.Leader, before.ContainerID)
}

func (d Driver) runS2(ctx context.Context, cluster inject.ClusterView) error {
	target := cluster.Leader
	if err := d.Faults.DemonstrateNamespacePID1Noop(ctx, target); err != nil {
		return err
	}
	oldInspection := inject.InspectNode(ctx, d.Faults, target)
	if !oldInspection.OK || oldInspection.Cgroup == "" {
		return fmt.Errorf("old container cgroup was not observed for %s", target.Pod)
	}
	before, err := d.Faults.Evidence(ctx, target)
	if err != nil {
		return err
	}
	if err := d.Faults.SignalContainerInit(ctx, target, "KILL"); err != nil {
		return err
	}
	if _, err := d.waitForRestart(ctx, target, before, 2*time.Minute); err != nil {
		return err
	}
	if err := d.capturePreviousLog(ctx, target, before.ContainerID); err != nil {
		return err
	}
	if err := d.waitForLeader(ctx, 2*time.Minute); err != nil {
		return err
	}
	postKillCluster, err := inject.DiscoverCluster(ctx, d.Commands, d.Context, d.Namespace)
	if err != nil {
		return err
	}
	postKillTarget, ok := targetByPod(postKillCluster.DatabasePods, target.Pod)
	if !ok {
		return fmt.Errorf("restarted Pod %s is absent from database inventory", target.Pod)
	}
	oldCount, err := inject.OldCgroupPostgresCount(ctx, d.Faults, postKillTarget, oldInspection.Cgroup)
	if err != nil {
		return err
	}
	if oldCount != 0 {
		return fmt.Errorf("old container cgroup %s still contains %d Postgres processes", oldInspection.Cgroup, oldCount)
	}

	refreshed, err := inject.DiscoverCluster(ctx, d.Commands, d.Context, d.Namespace)
	if err != nil {
		return err
	}
	target = refreshed.Leader
	d.captureDmesg(ctx, target.Node, "before")
	beforeOOM, err := d.Faults.Evidence(ctx, target)
	if err != nil {
		return err
	}
	if err := d.Faults.OOMContainer(ctx, target); err != nil {
		return err
	}
	d.captureDmesg(ctx, target.Node, "after")
	afterOOM, err := d.waitForChangeOrStability(ctx, target, beforeOOM, 30*time.Second)
	if err != nil {
		return err
	}
	if afterOOM.ContainerID != beforeOOM.ContainerID {
		if afterOOM.RestartCount != beforeOOM.RestartCount+1 {
			return fmt.Errorf("OOM restart count changed from %d to %d, want exactly one", beforeOOM.RestartCount, afterOOM.RestartCount)
		}
		if err := d.capturePreviousLog(ctx, target, beforeOOM.ContainerID); err != nil {
			return err
		}
	}
	if err := d.waitForLeader(ctx, 2*time.Minute); err != nil {
		return err
	}

	refreshed, err = inject.DiscoverCluster(ctx, d.Commands, d.Context, d.Namespace)
	if err != nil {
		return err
	}
	target = refreshed.Leader
	beforeTerm, err := d.Faults.Evidence(ctx, target)
	if err != nil {
		return err
	}
	if err := d.Faults.SignalContainerInit(ctx, target, "TERM"); err != nil {
		return err
	}
	if _, err = d.waitForRestart(ctx, target, beforeTerm, 2*time.Minute); err != nil {
		return err
	}
	return d.capturePreviousLog(ctx, target, beforeTerm.ContainerID)
}

func (d Driver) runS3(ctx context.Context, cluster inject.ClusterView) (returnErr error) {
	if err := ValidatePartitionDuration(d.PartitionDuration, cluster.ExplicitToleration); err != nil {
		return err
	}
	spec := inject.PartitionSpec{
		Target: cluster.Leader, OracleIP: cluster.OracleIP, ControlPlaneIP: cluster.ControlPlaneIP,
		PeerNodeIPs: cluster.PeerNodeIPs, PeerPodCIDRs: cluster.PeerPodCIDRs, ServiceAPIIP: cluster.ServiceAPIIP,
	}
	if err := d.Faults.PartitionNode(ctx, spec); err != nil {
		return err
	}
	healed := false
	defer func() {
		if healed {
			return
		}
		if err := d.Faults.HealNode(context.Background(), cluster.Leader); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	results, err := d.Marker.Probe(ctx, cluster.Leader.PodIP, []int{5432, 8008})
	if err != nil {
		return err
	}
	if results[5432] != "reachable" || results[8008] != "reachable" {
		return fmt.Errorf("oracle lost direct view of isolated Pod: %#v", results)
	}
	apiProbe := d.Commands.Run(ctx, []string{"docker", "exec", cluster.Leader.Node, "nc", "-z", "-w", "2", cluster.ControlPlaneIP, "6443"}, nil)
	if apiProbe.ExitCode == 0 {
		return fmt.Errorf("partition reachability check still reaches API server")
	}
	for _, peerIP := range cluster.PeerNodeIPs {
		peerProbe := d.Commands.Run(ctx, []string{"docker", "exec", cluster.Leader.Node, "nc", "-z", "-w", "2", peerIP, "10250"}, nil)
		if peerProbe.ExitCode == 0 {
			return fmt.Errorf("partition reachability check still reaches peer node %s", peerIP)
		}
	}
	timer := time.NewTimer(d.PartitionDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := d.Faults.HealNode(ctx, cluster.Leader); err != nil {
		return err
	}
	healed = true
	returnErr = nil
	return d.waitForLeader(ctx, 3*time.Minute)
}

func (d Driver) waitForRestart(ctx context.Context, target inject.Target, before inject.ContainerEvidence, timeout time.Duration) (inject.ContainerEvidence, error) {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return inject.ContainerEvidence{}, ctx.Err()
		case <-deadline.C:
			return inject.ContainerEvidence{}, fmt.Errorf("Pod %s did not restart within %s", target.Pod, timeout)
		case <-ticker.C:
			after, err := d.Faults.Evidence(ctx, target)
			if err != nil {
				continue
			}
			if after.ContainerID != before.ContainerID {
				if after.RestartCount != before.RestartCount+1 {
					return after, fmt.Errorf("restart count changed from %d to %d, want exactly one", before.RestartCount, after.RestartCount)
				}
				return after, nil
			}
		}
	}
}

func (d Driver) waitForChangeOrStability(ctx context.Context, target inject.Target, before inject.ContainerEvidence, timeout time.Duration) (inject.ContainerEvidence, error) {
	timer := time.NewTimer(timeout)
	ticker := time.NewTicker(time.Second)
	defer timer.Stop()
	defer ticker.Stop()
	last := before
	for {
		select {
		case <-ctx.Done():
			return inject.ContainerEvidence{}, ctx.Err()
		case <-timer.C:
			return last, nil
		case <-ticker.C:
			current, err := d.Faults.Evidence(ctx, target)
			if err == nil {
				last = current
				if current.ContainerID != before.ContainerID {
					return current, nil
				}
			}
		}
	}
}

func (d Driver) waitForLeader(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("exactly one discoverable leader did not converge within %s", timeout)
		case <-ticker.C:
			if _, err := inject.DiscoverCluster(ctx, d.Commands, d.Context, d.Namespace); err == nil {
				return nil
			}
		}
	}
}

func (d Driver) capturePreviousLog(ctx context.Context, target inject.Target, oldContainerID string) error {
	result := d.Commands.Run(ctx, []string{"kubectl", "--context", d.Context, "-n", d.Namespace, "logs", target.Pod, "-c", target.Container, "--previous"}, nil)
	if result.ExitCode != 0 {
		return fmt.Errorf("capture previous Patroni log exit %d: %s", result.ExitCode, result.Stderr)
	}
	name := fmt.Sprintf("patroni-%s-%s.log", target.Pod, sanitizeContainerID(oldContainerID))
	return writeArtifact(filepath.Join(d.BundleDir, "logs", name), []byte(result.Stdout))
}

func (d Driver) captureDmesg(ctx context.Context, node, phase string) {
	result := d.Commands.Run(ctx, []string{"docker", "exec", node, "dmesg", "--ctime"}, nil)
	if result.ExitCode == 0 {
		_ = writeArtifact(filepath.Join(d.BundleDir, "logs", fmt.Sprintf("node-%s-dmesg-%s.log", node, phase)), []byte(result.Stdout))
	}
}

func writeArtifact(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, inject.Redact(data), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func sanitizeContainerID(containerID string) string {
	if len(containerID) > 16 {
		return containerID[:16]
	}
	return containerID
}

func targetByPod(targets []inject.Target, pod string) (inject.Target, bool) {
	for _, target := range targets {
		if target.Pod == pod {
			return target, true
		}
	}
	return inject.Target{}, false
}
