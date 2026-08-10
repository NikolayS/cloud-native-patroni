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

package boundary

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// defaultMarkerSize is git's own conflict marker length, used when git passes a
// value that cannot be one.
const defaultMarkerSize = 7

// maxMergeFileConflicts is the highest conflict count `git merge-file` reports.
// Anything above it is an error status, not a conflict count.
const maxMergeFileConflicts = 127

// MergeInputs are the five arguments git substitutes into a merge driver
// command line: %O %A %B %L %P.
type MergeInputs struct {
	// Ancestor is git's %O, the common ancestor's version of the file.
	Ancestor string
	// Ours is git's %A. It carries the current version on the way in and the
	// merge result on the way out, which is why git keeps whatever the driver
	// leaves in it.
	Ours string
	// Theirs is git's %B, the other branch's version.
	Theirs string
	// MarkerSize is git's %L, the conflict marker length.
	MarkerSize int
	// Path is git's %P, the path in the worktree. It is used only for labels
	// and messages: the driver never reads it.
	Path string
}

// MergeDriver performs the three-way merge of one boundary file and reports
// whether git's own merge would have succeeded.
//
// It does not fabricate conflict markers when the merge is clean. The caller is
// what makes the merge unresolved, by exiting non-zero: git then records the
// three stages in the index and stops, so the maintainer has both the merged
// text and the original blobs to review. Inventing markers around a clean merge
// would destroy the auto-merged text, which is exactly what a reviewer needs to
// read.
//
// It never reports a clean merge for a run it could not complete. The exit
// status of a merge driver is git's only signal, and a driver that fails open
// grants exactly the silent absorption docs/cnpatroni/fork-maintenance.md
// records: upstream added 87 lines to pkg/management/postgres/instance.go and
// the trial merge reported a clean tree.
func MergeDriver(in MergeInputs) (bool, error) {
	if in.Ancestor == "" || in.Ours == "" || in.Theirs == "" {
		return false, fmt.Errorf(
			"the merge driver needs git's ancestor, ours and theirs temporary files (%%O %%A %%B)")
	}

	markerSize := in.MarkerSize
	if markerSize <= 0 {
		markerSize = defaultMarkerSize
	}

	label := in.Path
	if label == "" {
		label = "the boundary file"
	}

	// --diff3 keeps the common ancestor in the conflicted file, because a
	// boundary review has to answer what upstream changed, not only what the
	// two sides now say.
	cmd := exec.Command("git", "merge-file", //nolint:gosec // the arguments are git's own temporary files
		"--diff3",
		"--marker-size="+strconv.Itoa(markerSize),
		"-L", "ours (CloudNativePatroni): "+label,
		"-L", "base (last integrated upstream): "+label,
		"-L", "theirs (upstream): "+label,
		in.Ours, in.Ancestor, in.Theirs)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code > 0 && code <= maxMergeFileConflicts {
			return false, nil
		}
	}

	return false, fmt.Errorf("git merge-file on %s: %w: %s", label, err, strings.TrimSpace(string(out)))
}
