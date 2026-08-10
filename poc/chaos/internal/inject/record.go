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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Execution is the process result independent of recording timestamps.
type Execution struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Executor runs exact argv without invoking a shell.
type Executor interface {
	Execute(context.Context, []string, []byte) Execution
}

// CommandRecord is one auditable host or node command.
type CommandRecord struct {
	Argv      []string  `json:"argv"`
	ExitCode  int       `json:"exit_code"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// Recorder runs a command and incrementally appends its complete evidence.
type Recorder struct {
	path     string
	executor Executor
	now      func() time.Time
	mu       sync.Mutex
}

var redactionRules = []struct {
	pattern     *regexp.Regexp
	replacement []byte
}{
	{regexp.MustCompile(`(?im)(^\s*password\s*:\s*)("[^"]*"|'[^']*'|[^\s#]+)`), []byte(`${1}[REDACTED]`)},
	{regexp.MustCompile(`(?i)(primary_conninfo[^\n]*?\bpassword=)([^\s"']+)`), []byte(`${1}[REDACTED]`)},
	{regexp.MustCompile(`(?i)((https|http|postgresql|postgres)://[^:/@\s]+:)([^@\s]+)(@)`), []byte(`${1}[REDACTED]${4}`)},
}

// Redact removes password-bearing configuration and URL forms.
func Redact(input []byte) []byte {
	result := bytes.Clone(input)
	for _, rule := range redactionRules {
		result = rule.pattern.ReplaceAll(result, rule.replacement)
	}
	return result
}

// NewRecorder creates an owner-only faults stream.
func NewRecorder(path string, executor Executor, now func() time.Time) (*Recorder, error) {
	if path == "" {
		return nil, fmt.Errorf("fault record path is empty")
	}
	if executor == nil {
		executor = OSExecutor{}
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create fault record directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create fault record: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return &Recorder{path: path, executor: executor, now: now}, nil
}

// Run records exact argv, output, exit code, and absolute host timestamps.
func (r *Recorder) Run(ctx context.Context, argv []string, stdin []byte) Execution {
	started := r.now().UTC()
	result := Execution{}
	if len(argv) == 0 {
		result = Execution{ExitCode: 3, Stderr: "empty argv is not executable"}
	} else {
		result = r.executor.Execute(ctx, append([]string(nil), argv...), append([]byte(nil), stdin...))
	}
	ended := r.now().UTC()
	record := CommandRecord{Argv: append([]string(nil), argv...), ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr, StartedAt: started, EndedAt: ended}
	if err := r.append(record); err != nil {
		if result.Stderr != "" {
			result.Stderr += "; "
		}
		result.Stderr += "record command: " + err.Error()
		if result.ExitCode == 0 {
			result.ExitCode = 3
		}
	}
	return result
}

func (r *Recorder) append(record CommandRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	file, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(Redact(data)); err != nil {
		return err
	}
	return file.Sync()
}

// OSExecutor is the production exact-argv process adapter.
type OSExecutor struct{}

// Execute runs without a shell and preserves separate stdout and stderr.
func (OSExecutor) Execute(ctx context.Context, argv []string, stdin []byte) Execution {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := Execution{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = 3
		if result.Stderr != "" {
			result.Stderr += "; "
		}
		result.Stderr += err.Error()
	}
	return result
}
