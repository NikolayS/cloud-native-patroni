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

package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/cli"
	"go.yaml.in/yaml/v3"
)

type workflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Name string            `yaml:"name"`
			ID   string            `yaml:"id"`
			If   string            `yaml:"if"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

var goRunPattern = regexp.MustCompile(`\bgo\s+(-C\s+\S+\s+)?run\b`)

func readWorkflow(t *testing.T, name string) workflow {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("../../../../../.github/workflows", name))
	if err != nil {
		t.Fatalf("resolve workflow path %q: %v", name, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow at resolved absolute path %q: %v", path, err)
	}

	var parsed workflow
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse workflow at resolved absolute path %q: %v", path, err)
	}
	return parsed
}

func TestWorkflowNeverReadsAnExitStatusThroughGoRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		run   string
		match bool
	}{
		{name: "plain go run", run: "go run ./cmd/tool", match: true},
		{name: "go run with working directory", run: "go -C hack/cnpatroni/audit run . check", match: true},
		{name: "go test with working directory", run: "go -C hack/cnpatroni/audit test ./...", match: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := goRunPattern.MatchString(tc.run); got != tc.match {
				t.Fatalf("go-run detection for %q = %v, want %v", tc.run, got, tc.match)
			}
		})
	}

	syncWorkflow := readWorkflow(t, "cnpatroni-upstream-sync.yml")
	for _, job := range syncWorkflow.Jobs {
		for _, step := range job.Steps {
			if goRunPattern.MatchString(step.Run) {
				t.Errorf("step %q runs the tool through `go run`; go run reports 1 for every non-zero exit, so the tool's exit-code lattice does not survive it", step.Name)
			}
		}
	}

	for _, name := range []string{
		"cnpatroni-upstream-sync.yml",
		"cnpatroni-authority-audit.yml",
	} {
		parsed := readWorkflow(t, name)
		for _, job := range parsed.Jobs {
			for _, step := range job.Steps {
				if strings.Contains(step.Run, "GITHUB_OUTPUT") && goRunPattern.MatchString(step.Run) {
					t.Errorf("workflow %q step %q both invokes `go run` and writes to $GITHUB_OUTPUT; go run cannot preserve a tool's non-zero exit code", name, step.Name)
				}
			}
		}
	}
}

func TestWorkflowBuildsTheToolBeforeRunningIt(t *testing.T) {
	parsed := readWorkflow(t, "cnpatroni-upstream-sync.yml")
	const (
		buildCommand  = `go build -o "${RUNNER_TEMP}/cnpatroni-upstream"`
		binaryCommand = `"${RUNNER_TEMP}/cnpatroni-upstream"`
	)

	for _, jobName := range []string{"divergence-report", "boundary-guard"} {
		job, ok := parsed.Jobs[jobName]
		if !ok {
			t.Errorf("workflow has no job %q", jobName)
			continue
		}

		buildAt := -1
		for i, step := range job.Steps {
			if strings.Contains(step.Run, buildCommand) {
				buildAt = i
				break
			}
		}
		if buildAt == -1 {
			t.Errorf("job %q never builds the tool", jobName)
			continue
		}

		for _, step := range job.Steps[buildAt+1:] {
			if invokesUpstreamTool(step.Run) && !strings.Contains(step.Run, binaryCommand) {
				t.Errorf("job %q step %q does not invoke the tool through %s", jobName, step.Name, binaryCommand)
			}
		}
	}
}

func invokesUpstreamTool(script string) bool {
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "echo ") {
			continue
		}
		if strings.Contains(line, "cnpatroni-upstream") {
			return true
		}
	}
	return false
}

func TestWorkflowGateFailsClosed(t *testing.T) {
	parsed := readWorkflow(t, "cnpatroni-upstream-sync.yml")
	job, ok := parsed.Jobs["divergence-report"]
	if !ok {
		t.Fatal("workflow has no divergence-report job")
	}

	var gate *struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		If   string            `yaml:"if"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
	}
	for i := range job.Steps {
		step := &job.Steps[i]
		if strings.TrimSpace(step.If) == "always()" {
			if _, exists := step.Env["EXIT_CODE"]; exists {
				gate = step
				break
			}
		}
	}
	if gate == nil {
		t.Fatal("no always() gate step with an EXIT_CODE environment variable")
	}

	wantFailAt := strconv.Itoa(cli.ExitUndeclared)
	if got := gate.Env["FAIL_AT"]; got != wantFailAt {
		t.Errorf("gate FAIL_AT = %q, want %q from cli.ExitUndeclared", got, wantFailAt)
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	scriptPath := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(scriptPath, []byte(gate.Run), 0o600); err != nil {
		t.Fatalf("write gate script: %v", err)
	}

	cases := []struct {
		code     string
		wantFail bool
	}{
		{code: "0", wantFail: false},
		{code: "1", wantFail: false},
		{code: "2", wantFail: false},
		{code: "3", wantFail: true},
		{code: "4", wantFail: true},
		{code: "5", wantFail: true},
		{code: "6", wantFail: true},
		{code: "", wantFail: true},
		{code: "x", wantFail: true},
		{code: "-1", wantFail: true},
		{code: "03", wantFail: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("exit_code_%q", tc.code), func(t *testing.T) {
			cmd := exec.Command("bash", "-Eeuo", "pipefail", scriptPath)
			cmd.Env = gateEnvironment(gate.Env, tc.code)
			output, err := cmd.CombinedOutput()
			if tc.wantFail && err == nil {
				t.Fatalf("gate succeeded for EXIT_CODE=%q; output:\n%s", tc.code, output)
			}
			if !tc.wantFail && err != nil {
				t.Fatalf("gate failed for EXIT_CODE=%q: %v; output:\n%s", tc.code, err, output)
			}
		})
	}
}

func gateEnvironment(declared map[string]string, exitCode string) []string {
	overridden := make(map[string]struct{}, len(declared)+1)
	for key := range declared {
		overridden[key] = struct{}{}
	}
	overridden["EXIT_CODE"] = struct{}{}

	environment := make([]string, 0, len(os.Environ())+len(declared))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, exists := overridden[key]; !exists {
			environment = append(environment, item)
		}
	}
	for key, value := range declared {
		if key != "EXIT_CODE" {
			environment = append(environment, key+"="+value)
		}
	}
	return append(environment, "EXIT_CODE="+exitCode)
}

func TestWorkflowTouchesTheTrackingIssueOnlyWhenAReportExists(t *testing.T) {
	parsed := readWorkflow(t, "cnpatroni-upstream-sync.yml")
	job, ok := parsed.Jobs["divergence-report"]
	if !ok {
		t.Fatal("workflow has no divergence-report job")
	}

	var reportStep, issueStep *struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		If   string            `yaml:"if"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
	}
	for i := range job.Steps {
		step := &job.Steps[i]
		if step.ID == "report" {
			reportStep = step
		}
		if step.Name == "Update the tracking issue" {
			issueStep = step
		}
	}
	if reportStep == nil {
		t.Fatal("divergence-report has no report step")
	}
	if issueStep == nil {
		t.Fatal("divergence-report has no tracking-issue step")
	}

	if strings.Contains(issueStep.If, "!= '0'") {
		t.Error("the tracking issue step fires on `exit_code != '0'`, which includes codes 5 and 6, where no report was written")
	}
	if !strings.Contains(issueStep.If, ".outputs.report_ready") {
		t.Errorf("tracking-issue condition %q does not reference the report_ready output", issueStep.If)
	}
	if !strings.Contains(reportStep.Run, "report_ready=") {
		t.Error("the report step does not set the report_ready output")
	}
	if !strings.Contains(reportStep.Run, `-s "${RUNNER_TEMP}/report.md"`) {
		t.Error("the report step does not require a non-empty report before marking it ready")
	}
}
