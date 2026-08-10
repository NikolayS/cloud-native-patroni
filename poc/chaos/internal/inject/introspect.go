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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StartIntrospection samples both kubectl exec and the node namespace every
// five seconds until the returned stop function completes.
func StartIntrospection(parent context.Context, faults Faults, marker Marker, targets []Target) func() {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			for _, target := range targets {
				sampleContainer(ctx, faults, marker, target)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

// VerifyPID1 takes both required views before any scenario begins.
func VerifyPID1(ctx context.Context, faults Faults, marker Marker, targets []Target) error {
	for _, target := range targets {
		nodeObservation := inspectFromNode(ctx, faults, target)
		if err := marker.ObserveContainer(ctx, nodeObservation); err != nil {
			return err
		}
		kubectlObservation := inspectWithKubectl(ctx, faults, target)
		if err := marker.ObserveContainer(ctx, kubectlObservation); err != nil {
			return err
		}
		for _, observation := range []ContainerObservation{nodeObservation, kubectlObservation} {
			if !observation.OK || observation.PID1Comm != "patroni" {
				return fmt.Errorf("Pod %s source %s PID 1 comm=%q ok=%t", target.Pod, observation.Source, observation.PID1Comm, observation.OK)
			}
		}
	}
	return nil
}

func sampleContainer(ctx context.Context, faults Faults, marker Marker, target Target) {
	nodeObservation := inspectFromNode(ctx, faults, target)
	_ = marker.ObserveContainer(ctx, nodeObservation)
	kubectlObservation := inspectWithKubectl(ctx, faults, target)
	_ = marker.ObserveContainer(ctx, kubectlObservation)
}

func inspectFromNode(ctx context.Context, faults Faults, target Target) ContainerObservation {
	observation := ContainerObservation{Node: target.Pod, Source: "docker"}
	data, err := os.ReadFile(filepath.Join(faults.ScriptDir, "inspect-container.sh"))
	if err != nil {
		observation.Error = err.Error()
		return observation
	}
	result := faults.Commands.Run(ctx, []string{"docker", "exec", "-i", target.Node, "bash", "-s", "--", target.ContainerID}, data)
	observation.Raw = result.Stdout
	if result.ExitCode != 0 {
		observation.Error = fmt.Sprintf("inspect script exit %d: %s", result.ExitCode, result.Stderr)
		return observation
	}
	values := parseInspection(result.Stdout)
	observation.PID1Comm = values["comm"]
	observation.PID1Cmdline = values["cmdline"]
	observation.Cgroup = values["cgroup"]
	observation.ZombieCount, _ = strconv.Atoi(values["zombie_count"])
	observation.OK = observation.PID1Comm != ""
	if !observation.OK {
		observation.Error = "node inspection omitted comm"
	}
	return observation
}

// InspectNode returns one recorded node-namespace observation.
func InspectNode(ctx context.Context, faults Faults, target Target) ContainerObservation {
	return inspectFromNode(ctx, faults, target)
}

// OldCgroupPostgresCount proves whether a Postgres process remains attached to
// a prior container cgroup after namespace replacement.
func OldCgroupPostgresCount(ctx context.Context, faults Faults, target Target, oldCgroup string) (int, error) {
	data, err := os.ReadFile(filepath.Join(faults.ScriptDir, "inspect-container.sh"))
	if err != nil {
		return 0, err
	}
	result := faults.Commands.Run(ctx, []string{"docker", "exec", "-i", target.Node, "bash", "-s", "--", target.ContainerID, oldCgroup}, data)
	if result.ExitCode != 0 {
		return 0, fmt.Errorf("old cgroup inspection exit %d: %s", result.ExitCode, result.Stderr)
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if value, ok := strings.CutPrefix(line, "old_cgroup_postgres_count="); ok {
			count, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return 0, parseErr
			}
			return count, nil
		}
	}
	return 0, fmt.Errorf("old cgroup inspection omitted Postgres count")
}

func inspectWithKubectl(ctx context.Context, faults Faults, target Target) ContainerObservation {
	observation := ContainerObservation{Node: target.Pod, Source: "kubectl"}
	base := []string{"kubectl", "--context", faults.Context, "-n", faults.Namespace, "exec", target.Pod, "-c", target.Container, "--"}
	comm := faults.Commands.Run(ctx, append(append([]string(nil), base...), "cat", "/proc/1/comm"), nil)
	cmdline := faults.Commands.Run(ctx, append(append([]string(nil), base...), "cat", "/proc/1/cmdline"), nil)
	processes := faults.Commands.Run(ctx, append(append([]string(nil), base...), "ps", "-eo", "pid,ppid,stat,comm"), nil)
	observation.Raw = comm.Stdout + "\n" + cmdline.Stdout + "\n" + processes.Stdout
	if comm.ExitCode != 0 || cmdline.ExitCode != 0 || processes.ExitCode != 0 {
		observation.Error = fmt.Sprintf("kubectl exec exits comm=%d cmdline=%d ps=%d", comm.ExitCode, cmdline.ExitCode, processes.ExitCode)
		return observation
	}
	observation.OK = true
	observation.PID1Comm = strings.TrimSpace(comm.Stdout)
	observation.PID1Cmdline = strings.ReplaceAll(cmdline.Stdout, "\x00", " ")
	for index, line := range strings.Split(processes.Stdout, "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.HasPrefix(fields[2], "Z") {
			observation.ZombieCount++
		}
	}
	return observation
}

func parseInspection(raw string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		if line == "processes_begin" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}
