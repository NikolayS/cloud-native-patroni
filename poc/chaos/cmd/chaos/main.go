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

// Command chaos drives removal-only kind faults and records their evidence.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/chaos/internal/inject"
	"github.com/cloudnative-pg/cloudnative-pg/poc/chaos/internal/scenario"
)

type config struct {
	Context           string
	Namespace         string
	Scenario          string
	PartitionDuration time.Duration
	BundleDir         string
}

type hostManifest struct {
	RunID             string     `json:"run_id"`
	Scenario          string     `json:"scenario"`
	Start             time.Time  `json:"start"`
	End               *time.Time `json:"end,omitempty"`
	GitCommit         string     `json:"git_commit,omitempty"`
	KubeContext       string     `json:"kube_context"`
	Namespace         string     `json:"namespace"`
	PartitionDuration string     `json:"partition_duration,omitempty"`
	HarnessError      string     `json:"harness_error,omitempty"`
}

func main() { os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr)) }

func runCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("chaos", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config config
	flags.StringVar(&config.Context, "context", "", "mandatory kind Kubernetes context")
	flags.StringVar(&config.Namespace, "namespace", "", "database namespace")
	flags.StringVar(&config.Scenario, "scenario", "", "s1-primary-crash, s2-sigkill-oom, or s3-partition")
	flags.DurationVar(&config.PartitionDuration, "partition-duration", 120*time.Second, "S3 partition duration")
	flags.StringVar(&config.BundleDir, "bundle-dir", "", "run bundle parent directory")
	if err := flags.Parse(args); err != nil {
		return 3
	}
	if err := config.validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}

	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	started := time.Now()
	runID, err := scenario.RunID(config.Scenario, started, random)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	runDir := filepath.Join(config.BundleDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	recorder, err := inject.NewRecorder(filepath.Join(runDir, "faults.jsonl"), nil, time.Now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	manifest := hostManifest{RunID: runID, Scenario: config.Scenario, Start: started.UTC(), KubeContext: config.Context, Namespace: config.Namespace}
	if config.Scenario == "s3-partition" {
		manifest.PartitionDuration = config.PartitionDuration.String()
	}
	gitResult := recorder.Run(context.Background(), []string{"git", "rev-parse", "HEAD"}, nil)
	if gitResult.ExitCode == 0 {
		manifest.GitCommit = strings.TrimSpace(gitResult.Stdout)
	}
	_ = writeJSON(filepath.Join(runDir, "host-manifest.json"), manifest)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	guard := inject.ContextGuard{Context: config.Context, Commands: recorder}
	if err := guard.Verify(ctx); err != nil {
		return finishHarnessError(stderr, runDir, manifest, err)
	}
	cluster, err := inject.DiscoverCluster(ctx, recorder, config.Context, config.Namespace)
	if err != nil {
		return finishHarnessError(stderr, runDir, manifest, err)
	}
	if config.Scenario == "s3-partition" {
		if err := scenario.ValidatePartitionDuration(config.PartitionDuration, cluster.ExplicitToleration); err != nil {
			return finishHarnessError(stderr, runDir, manifest, err)
		}
	}
	forward, err := inject.StartPortForward(ctx, recorder, config.Context, config.Namespace, cluster.OraclePod, 9099)
	if err != nil {
		return finishHarnessError(stderr, runDir, manifest, err)
	}
	marker := inject.HTTPMarker{BaseURL: forward.URL}
	scriptDir, err := findScriptDir()
	if err != nil {
		forward.Close()
		return finishHarnessError(stderr, runDir, manifest, err)
	}
	faults := inject.Faults{Context: config.Context, Namespace: config.Namespace, Commands: recorder, Guard: guard, Marker: marker, ScriptDir: scriptDir}
	if err := inject.VerifyPID1(ctx, faults, marker, cluster.DatabasePods); err != nil {
		forward.Close()
		return finishHarnessError(stderr, runDir, manifest, err)
	}
	driver := scenario.Driver{
		Context: config.Context, Namespace: config.Namespace, Name: config.Scenario,
		PartitionDuration: config.PartitionDuration, BundleDir: runDir,
		Commands: recorder, Faults: faults, Marker: marker,
	}
	stopIntrospection := inject.StartIntrospection(ctx, faults, marker, cluster.DatabasePods)
	scenarioErr := driver.Run(ctx, cluster)
	stopIntrospection()
	forward.Close()
	logTargets := cluster.DatabasePods
	if refreshed, discoverErr := inject.DiscoverCluster(ctx, recorder, config.Context, config.Namespace); discoverErr == nil {
		logTargets = refreshed.DatabasePods
	}
	capturePatroniLogs(ctx, recorder, config, logTargets, runDir)
	captureKubernetes(ctx, recorder, config, runDir)
	waitForOracleCompletion(ctx, recorder, config, cluster.OraclePod)
	copyErr := copyOracleBundle(ctx, recorder, config, cluster.OraclePod, runDir)
	ended := time.Now().UTC()
	manifest.End = &ended
	if scenarioErr != nil {
		manifest.HarnessError = scenarioErr.Error()
	} else if copyErr != nil {
		manifest.HarnessError = copyErr.Error()
	}
	_ = writeJSON(filepath.Join(runDir, "host-manifest.json"), manifest)
	if scenarioErr != nil {
		fmt.Fprintln(stderr, scenarioErr)
		fmt.Fprintln(stdout, runDir)
		return 3
	}
	if copyErr != nil {
		fmt.Fprintln(stderr, copyErr)
		fmt.Fprintln(stdout, runDir)
		return 3
	}
	exit := readOracleExit(filepath.Join(runDir, "verdict.json"))
	fmt.Fprintln(stdout, runDir)
	return exit
}

func capturePatroniLogs(ctx context.Context, recorder *inject.Recorder, config config, targets []inject.Target, runDir string) {
	_ = os.MkdirAll(filepath.Join(runDir, "logs"), 0o700)
	for _, target := range targets {
		containerID := target.ContainerID
		if len(containerID) > 16 {
			containerID = containerID[:16]
		}
		current := recorder.Run(ctx, []string{"kubectl", "--context", config.Context, "-n", config.Namespace, "logs", target.Pod, "-c", target.Container}, nil)
		if current.ExitCode == 0 {
			_ = os.WriteFile(filepath.Join(runDir, "logs", "patroni-"+target.Pod+"-"+containerID+".log"), inject.Redact([]byte(current.Stdout)), 0o600)
		}
		previous := recorder.Run(ctx, []string{"kubectl", "--context", config.Context, "-n", config.Namespace, "logs", target.Pod, "-c", target.Container, "--previous"}, nil)
		if previous.ExitCode == 0 {
			_ = os.WriteFile(filepath.Join(runDir, "logs", "patroni-"+target.Pod+"-previous.log"), inject.Redact([]byte(previous.Stdout)), 0o600)
		}
	}
}

func (c config) validate() error {
	if !strings.HasPrefix(c.Context, "kind-") || len(c.Context) == len("kind-") {
		return fmt.Errorf("--context is mandatory and must match ^kind-")
	}
	if c.Namespace == "" || c.BundleDir == "" {
		return fmt.Errorf("--namespace and --bundle-dir are mandatory")
	}
	switch c.Scenario {
	case "s1-primary-crash", "s2-sigkill-oom":
	case "s3-partition":
		if c.PartitionDuration <= 0 {
			return fmt.Errorf("--partition-duration must be positive")
		}
	default:
		return fmt.Errorf("--scenario must be s1-primary-crash, s2-sigkill-oom, or s3-partition")
	}
	return nil
}

func findScriptDir() (string, error) {
	if configured := os.Getenv("CNPATRONI_CHAOS_SCRIPT_DIR"); configured != "" {
		return configured, nil
	}
	for _, candidate := range []string{"poc/chaos/scripts", "scripts", "../../scripts"} {
		if info, err := os.Stat(filepath.Join(candidate, "signal-container-init.sh")); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("node-side script directory not found; set CNPATRONI_CHAOS_SCRIPT_DIR")
}

func captureKubernetes(ctx context.Context, recorder *inject.Recorder, config config, runDir string) {
	objects := [][]string{
		{"-n", config.Namespace, "get", "endpoints", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "endpointslices.discovery.k8s.io", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "services", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "pods", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "statefulsets.apps", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "configmaps", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "role", "cnpatroni-oracle", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "rolebinding", "cnpatroni-oracle", "-o", "yaml", "--show-managed-fields"},
		{"-n", config.Namespace, "get", "secrets", "-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}"},
	}
	for index, suffix := range objects {
		argv := []string{"kubectl", "--context", config.Context}
		argv = append(argv, suffix...)
		result := recorder.Run(ctx, argv, nil)
		if result.ExitCode == 0 {
			name := fmt.Sprintf("snapshot-%02d.yaml", index+1)
			if index == len(objects)-1 {
				name = "secret-names.txt"
			}
			_ = os.MkdirAll(filepath.Join(runDir, "k8s"), 0o700)
			_ = os.WriteFile(filepath.Join(runDir, "k8s", name), inject.Redact([]byte(result.Stdout)), 0o600)
		}
	}
}

func waitForOracleCompletion(ctx context.Context, recorder *inject.Recorder, config config, pod string) {
	deadline := time.NewTimer(15 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			result := recorder.Run(ctx, []string{"kubectl", "--context", config.Context, "-n", config.Namespace, "get", "pod", pod, "-o", "jsonpath={.status.phase}"}, nil)
			if result.ExitCode == 0 && (result.Stdout == "Succeeded" || result.Stdout == "Failed") {
				return
			}
		}
	}
}

func copyOracleBundle(ctx context.Context, recorder *inject.Recorder, config config, pod, runDir string) error {
	temporary := filepath.Join(runDir, ".oracle-copy")
	result := recorder.Run(ctx, []string{"kubectl", "--context", config.Context, "-n", config.Namespace, "cp", pod + ":/var/run/cnpatroni-oracle/current", temporary}, nil)
	if result.ExitCode != 0 {
		return fmt.Errorf("copy oracle bundle exit %d: %s", result.ExitCode, result.Stderr)
	}
	source := temporary
	if info, err := os.Stat(filepath.Join(temporary, "current")); err == nil && info.IsDir() {
		source = filepath.Join(temporary, "current")
	}
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		destination := filepath.Join(runDir, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if strings.HasPrefix(temporary, filepath.Clean(runDir)+string(os.PathSeparator)) {
		_ = os.RemoveAll(temporary)
	}
	return err
}

func readOracleExit(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 3
	}
	var result struct {
		ExitCode int `json:"exit_code"`
	}
	if json.Unmarshal(data, &result) != nil || result.ExitCode < 0 || result.ExitCode > 3 {
		return 3
	}
	return result.ExitCode
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func finishHarnessError(stderr io.Writer, runDir string, manifest hostManifest, err error) int {
	ended := time.Now().UTC()
	manifest.End, manifest.HarnessError = &ended, err.Error()
	_ = writeJSON(filepath.Join(runDir, "host-manifest.json"), manifest)
	fmt.Fprintln(stderr, err)
	return 3
}
