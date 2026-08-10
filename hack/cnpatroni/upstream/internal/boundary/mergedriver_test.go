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

package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
)

// mergeInputs writes the three temporary files git hands a merge driver.
func mergeInputs(t *testing.T, ancestor, ours, theirs string) boundary.MergeInputs {
	t.Helper()

	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}

		return path
	}

	return boundary.MergeInputs{
		Ancestor:   write("ancestor", ancestor),
		Ours:       write("ours", ours),
		Theirs:     write("theirs", theirs),
		MarkerSize: 7,
		Path:       "pkg/management/postgres/instance.go",
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // the path is a test temporary file
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	return string(body)
}

// This is the case docs/cnpatroni/fork-maintenance.md records: upstream changes
// one end of a boundary file, the fork changed the other, and git merges the two
// without a word. The driver has to report the merge as unresolved anyway.
func TestMergeDriverReportsACleanThreeWayMerge(t *testing.T) {
	in := mergeInputs(t,
		"package postgres\n\nfunc a() {}\nfunc b() {}\n",
		"package postgres\n\nfunc adapted() {}\nfunc b() {}\n",
		"package postgres\n\nfunc a() {}\nfunc b() {}\nfunc upstreamAddition() {}\n")

	clean, err := boundary.MergeDriver(in)
	if err != nil {
		t.Fatalf("MergeDriver: %v", err)
	}
	if !clean {
		t.Fatal("git's own three-way merge succeeds here, so the driver must report the merge as clean")
	}

	merged := readFile(t, in.Ours)
	for _, want := range []string{"func adapted() {}", "func upstreamAddition() {}"} {
		if !strings.Contains(merged, want) {
			t.Errorf("the merged file lost %q:\n%s", want, merged)
		}
	}
	if strings.Contains(merged, "<<<<<<<") {
		t.Errorf("a clean merge must not be given fabricated conflict markers:\n%s", merged)
	}
}

func TestMergeDriverKeepsGitsOwnConflictMarkers(t *testing.T) {
	in := mergeInputs(t,
		"package postgres\n\nfunc a() {}\n",
		"package postgres\n\nfunc ours() {}\n",
		"package postgres\n\nfunc theirs() {}\n")

	clean, err := boundary.MergeDriver(in)
	if err != nil {
		t.Fatalf("MergeDriver: %v", err)
	}
	if clean {
		t.Fatal("the two sides changed the same line, so the merge is not clean")
	}

	merged := readFile(t, in.Ours)
	for _, want := range []string{"<<<<<<<", "|||||||", "=======", ">>>>>>>"} {
		if !strings.Contains(merged, want) {
			t.Errorf("the conflicted file is missing the %q marker:\n%s", want, merged)
		}
	}
}

// The labels are the only thing telling a maintainer which side of a boundary
// hunk is upstream's, so they have to name both the side and the path.
func TestMergeDriverLabelsBothSidesAndThePath(t *testing.T) {
	in := mergeInputs(t,
		"package postgres\n\nfunc a() {}\n",
		"package postgres\n\nfunc ours() {}\n",
		"package postgres\n\nfunc theirs() {}\n")

	if _, err := boundary.MergeDriver(in); err != nil {
		t.Fatalf("MergeDriver: %v", err)
	}

	merged := readFile(t, in.Ours)
	for _, want := range []string{"CloudNativePatroni", "upstream", in.Path} {
		if !strings.Contains(merged, want) {
			t.Errorf("the conflict labels do not mention %q:\n%s", want, merged)
		}
	}
}

func TestMergeDriverHonoursGitsMarkerSize(t *testing.T) {
	in := mergeInputs(t,
		"package postgres\n\nfunc a() {}\n",
		"package postgres\n\nfunc ours() {}\n",
		"package postgres\n\nfunc theirs() {}\n")
	in.MarkerSize = 10

	if _, err := boundary.MergeDriver(in); err != nil {
		t.Fatalf("MergeDriver: %v", err)
	}

	if merged := readFile(t, in.Ours); !strings.Contains(merged, strings.Repeat("<", 10)) {
		t.Errorf("the marker size git asked for was ignored:\n%s", merged)
	}
}

// git passes %L verbatim, and a merge driver registered by hand can be given a
// nonsense value. Falling back to git's default is better than failing open.
func TestMergeDriverFallsBackToTheDefaultMarkerSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		in := mergeInputs(t,
			"package postgres\n\nfunc a() {}\n",
			"package postgres\n\nfunc ours() {}\n",
			"package postgres\n\nfunc theirs() {}\n")
		in.MarkerSize = size

		if _, err := boundary.MergeDriver(in); err != nil {
			t.Fatalf("MergeDriver with marker size %d: %v", size, err)
		}

		merged := readFile(t, in.Ours)
		if !strings.Contains(merged, strings.Repeat("<", 7)) {
			t.Errorf("marker size %d did not fall back to 7:\n%s", size, merged)
		}
		if strings.Contains(merged, strings.Repeat("<", 8)) {
			t.Errorf("marker size %d produced a longer marker than 7:\n%s", size, merged)
		}
	}
}

func TestMergeDriverRejectsIncompleteInputs(t *testing.T) {
	complete := mergeInputs(t, "a\n", "b\n", "c\n")

	cases := map[string]func(in *boundary.MergeInputs){
		"no_ancestor": func(in *boundary.MergeInputs) { in.Ancestor = "" },
		"no_ours":     func(in *boundary.MergeInputs) { in.Ours = "" },
		"no_theirs":   func(in *boundary.MergeInputs) { in.Theirs = "" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := complete
			mutate(&in)

			clean, err := boundary.MergeDriver(in)
			if err == nil {
				t.Fatal("an incomplete invocation must be an error, not a clean merge")
			}
			if clean {
				t.Error("a failed driver must never report a clean merge")
			}
		})
	}
}

// A driver that cannot read git's temporary files has not merged anything, and
// must say so rather than reporting success.
func TestMergeDriverReportsAnUnreadableInput(t *testing.T) {
	in := mergeInputs(t, "a\n", "b\n", "c\n")
	in.Theirs = filepath.Join(t.TempDir(), "absent")

	clean, err := boundary.MergeDriver(in)
	if err == nil {
		t.Fatal("expected an error for an unreadable input, got none")
	}
	if clean {
		t.Error("a failed driver must never report a clean merge")
	}
}
