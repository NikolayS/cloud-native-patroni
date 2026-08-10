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

// Command oracle runs, replays, or preflights the direct-Pod write oracle.
package main

import (
	"context"
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

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/app"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/replay"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/secret"
)

type commonFlags struct {
	context           string
	namespace         string
	scope             string
	expectedNodes     string
	roundPeriod       time.Duration
	duration          time.Duration
	bundleDir         string
	controlPort       int
	patroniPort       int
	pgPort            int
	preflightRounds   int
	lostWriteSeverity string
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oracle run | replay | preflight")
		return 3
	}
	switch args[0] {
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "run":
		return runMeasurement(args[1:], stdout, stderr)
	case "preflight":
		return runPreflight(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown oracle subcommand %q\n", args[0])
		return 3
	}
}

func runReplay(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("oracle replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fixture := flags.String("fixture", "", "recorded fixture directory")
	if err := flags.Parse(args); err != nil {
		return 3
	}
	if *fixture == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--fixture is mandatory and positional arguments are not accepted")
		return 3
	}
	result, err := replay.LoadAndDetect(*fixture)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	fmt.Fprintln(stdout, string(data))
	return result.ExitCode
}

func runMeasurement(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("oracle run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	common := registerCommon(flags, true)
	if err := flags.Parse(args); err != nil {
		return 3
	}
	config, err := common.config()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	password, err := loadPassword("CNPATRONI_CHAOS_PASSWORD_FILE", "CNPATRONI_CHAOS_PASSWORD")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result := app.RunLive(ctx, config, password)
	if result.Err != nil {
		fmt.Fprintln(stderr, result.Err)
	}
	data, marshalErr := json.Marshal(result.Verdict)
	if marshalErr != nil {
		fmt.Fprintln(stderr, marshalErr)
		return 3
	}
	fmt.Fprintln(stdout, string(data))
	return result.Verdict.ExitCode
}

func runPreflight(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("oracle preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	common := registerCommon(flags, false)
	bootstrap := flags.Bool("bootstrap", false, "create the oracle role and scratch table once on the current leader")
	if err := flags.Parse(args); err != nil {
		return 3
	}
	config, err := common.config()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	fixtureRoot := os.Getenv("CNPATRONI_FIXTURE_ROOT")
	if fixtureRoot == "" {
		fixtureRoot = "/usr/share/cnpatroni-oracle/fixtures"
	}
	if err := verifyFixtures(fixtureRoot); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	chaosPassword, err := loadPassword("CNPATRONI_CHAOS_PASSWORD_FILE", "CNPATRONI_CHAOS_PASSWORD")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *bootstrap {
		superPassword, loadErr := loadPassword("CNPATRONI_SUPERUSER_PASSWORD_FILE", "CNPATRONI_SUPERUSER_PASSWORD")
		if loadErr != nil {
			fmt.Fprintln(stderr, loadErr)
			return 3
		}
		if err := app.BootstrapLive(ctx, config, chaosPassword, superPassword); err != nil {
			fmt.Fprintln(stderr, err)
			return 3
		}
		fmt.Fprintln(stdout, `{"bootstrap":"verified"}`)
		return 0
	}
	result := app.RunLive(ctx, config, chaosPassword)
	if result.Err != nil {
		fmt.Fprintln(stderr, result.Err)
	}
	data, marshalErr := json.Marshal(result.Verdict)
	if marshalErr != nil {
		fmt.Fprintln(stderr, marshalErr)
		return 3
	}
	fmt.Fprintln(stdout, string(data))
	return result.Verdict.ExitCode
}

func registerCommon(flags *flag.FlagSet, run bool) *commonFlags {
	result := &commonFlags{}
	flags.StringVar(&result.context, "context", "", "mandatory kind Kubernetes context")
	flags.StringVar(&result.namespace, "namespace", "", "database namespace")
	flags.StringVar(&result.scope, "scope", "", "Patroni scope and -rw Endpoints name")
	flags.StringVar(&result.expectedNodes, "expected-nodes", "", "comma-separated database Pod names")
	flags.DurationVar(&result.roundPeriod, "round-period", 500*time.Millisecond, "measurement round period, at most 1s")
	if run {
		flags.DurationVar(&result.duration, "duration", 0, "measurement duration")
	} else {
		result.duration = time.Second
	}
	flags.StringVar(&result.bundleDir, "bundle-dir", "", "incremental artifact bundle directory")
	flags.IntVar(&result.controlPort, "control-port", 9099, "oracle fault-marker control port")
	flags.IntVar(&result.patroniPort, "patroni-port", 8008, "Patroni REST port")
	flags.IntVar(&result.pgPort, "pg-port", 5432, "Postgres port")
	flags.IntVar(&result.preflightRounds, "preflight-rounds", 20, "required consecutive healthy rounds")
	flags.StringVar(&result.lostWriteSeverity, "lost-write-severity", "warn", "warn or fatal")
	return result
}

func (f *commonFlags) config() (app.Config, error) {
	var expected []string
	for _, node := range strings.Split(f.expectedNodes, ",") {
		if trimmed := strings.TrimSpace(node); trimmed != "" {
			expected = append(expected, trimmed)
		}
	}
	config := app.Config{
		Context: f.context, Namespace: f.namespace, Scope: f.scope, ExpectedNodes: expected,
		RoundPeriod: f.roundPeriod, Duration: f.duration, BundleDir: f.bundleDir,
		ControlPort: f.controlPort, PatroniPort: f.patroniPort, PGPort: f.pgPort,
		PreflightRounds: f.preflightRounds, LostWriteSeverity: f.lostWriteSeverity,
	}
	return config, config.Validate()
}

func loadPassword(pathEnv, valueEnv string) (string, error) {
	path := os.Getenv(pathEnv)
	if path != "" {
		if _, set := os.LookupEnv(valueEnv); set {
			return "", fmt.Errorf("%s and %s are mutually exclusive", pathEnv, valueEnv)
		}
		return secret.Load(path, "", os.LookupEnv)
	}
	return secret.Load("", valueEnv, os.LookupEnv)
}

func verifyFixtures(root string) error {
	want := map[string]int{"dual-commit-run": 1, "clean-run": 0, "indoubt-run": 2}
	for name, exit := range want {
		result, err := replay.LoadAndDetect(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("replay positive controls: %w", err)
		}
		if result.ExitCode != exit {
			return fmt.Errorf("fixture %s exit=%d, want %d", name, result.ExitCode, exit)
		}
	}
	return nil
}
