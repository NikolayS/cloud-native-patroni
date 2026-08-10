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
	"strings"
	"testing"
)

func TestContextGuardUsesExplicitContext(t *testing.T) {
	executor := &fakeExecutor{result: Execution{ExitCode: 0, Stdout: `{"items":[{"metadata":{"name":"demo-worker"},"spec":{"providerID":"kind://docker/demo/demo-worker"}}]}`}}
	recorder, err := NewRecorder(t.TempDir()+"/faults.jsonl", executor, nil)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	guard := ContextGuard{Context: "kind-demo", Commands: recorder}
	if err := guard.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	want := []string{"kubectl", "--context", "kind-demo", "get", "nodes", "-o", "json"}
	if strings.Join(executor.argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %#v, want %#v", executor.argv, want)
	}
}

func TestContextGuardRejectsCommandFailureAndMalformedJSON(t *testing.T) {
	for _, result := range []Execution{{ExitCode: 1, Stderr: "forbidden"}, {ExitCode: 0, Stdout: "{"}} {
		executor := &fakeExecutor{result: result}
		recorder, err := NewRecorder(t.TempDir()+"/faults.jsonl", executor, nil)
		if err != nil {
			t.Fatalf("NewRecorder() error = %v", err)
		}
		if err := (ContextGuard{Context: "kind-demo", Commands: recorder}).Verify(context.Background()); err == nil {
			t.Fatal("Verify() error = nil, want rejection")
		}
	}
}
