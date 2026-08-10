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

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestContextIsMandatoryAndHasNoDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"--namespace", "database", "--scenario", "s1-primary-crash", "--bundle-dir", "runs"}, &stdout, &stderr)
	if exit != 3 || !strings.Contains(stderr.String(), "--context") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestScenarioAllowlist(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"--context", "kind-demo", "--namespace", "database", "--scenario", "promote", "--bundle-dir", "runs"}, &stdout, &stderr)
	if exit != 3 || !strings.Contains(stderr.String(), "scenario") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestNonPositivePartitionDurationIsCheckedBeforeCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"--context", "kind-demo", "--namespace", "database", "--scenario", "s3-partition", "--partition-duration", "0s", "--bundle-dir", "runs"}, &stdout, &stderr)
	if exit != 3 || !strings.Contains(stderr.String(), "positive") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}
