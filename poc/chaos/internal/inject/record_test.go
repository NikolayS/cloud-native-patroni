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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeExecutor struct {
	argv   []string
	stdin  []byte
	result Execution
}

func (f *fakeExecutor) Execute(_ context.Context, argv []string, stdin []byte) Execution {
	f.argv, f.stdin = append([]string(nil), argv...), append([]byte(nil), stdin...)
	return f.result
}

func TestRecorderCapturesExactCommandAndRestrictedFile(t *testing.T) {
	executor := &fakeExecutor{result: Execution{ExitCode: 17, Stdout: "out", Stderr: "err"}}
	times := []time.Time{
		time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 9, 12, 0, 1, 0, time.UTC),
	}
	path := filepath.Join(t.TempDir(), "faults.jsonl")
	recorder, err := NewRecorder(path, executor, func() time.Time {
		result := times[0]
		times = times[1:]
		return result
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	got := recorder.Run(context.Background(), []string{"docker", "exec", "demo-worker", "kill", "-9", "123"}, nil)
	if got.ExitCode != 17 || got.Stdout != "out" || got.Stderr != "err" {
		t.Fatalf("Run() = %#v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{`"argv":["docker","exec","demo-worker","kill","-9","123"]`, `"exit_code":17`, `"stdout":"out"`, `"stderr":"err"`, `"started_at":"2026-08-09T12:00:00Z"`, `"ended_at":"2026-08-09T12:00:01Z"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("fault record %q does not contain %q", data, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o, want 0600", info.Mode().Perm())
	}
}

func TestRecorderRejectsEmptyArgv(t *testing.T) {
	recorder, err := NewRecorder(filepath.Join(t.TempDir(), "faults.jsonl"), &fakeExecutor{}, time.Now)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	got := recorder.Run(context.Background(), nil, nil)
	if got.ExitCode != 3 || !strings.Contains(got.Stderr, "empty argv") {
		t.Fatalf("Run(nil) = %#v", got)
	}
}
