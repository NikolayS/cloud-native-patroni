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
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
)

func renderAttributes(t *testing.T, body string) string {
	t.Helper()

	m, err := boundary.Parse([]byte(body))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}

	return boundary.GitAttributes(m)
}

func TestGitAttributesGuardsEveryReviewedClass(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, validRules, ""))

	for _, want := range []string{
		"pkg/specs/pods.go cnpatroni-boundary=adapted merge=" + boundary.MergeDriverName,
		"internal/controller/replicas.go cnpatroni-boundary=disabled merge=" + boundary.MergeDriverName,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered attributes are missing %q:\n%s", want, out)
		}
	}
}

// The catch-all must never appear: an attribute line for `**` would route every
// upstream file in the repository through the always-conflict driver.
func TestGitAttributesOmitsTheCatchAll(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, validRules, ""))

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "** ") || line == "**" {
			t.Errorf("the catch-all rule produced an attribute line: %q", line)
		}
	}
}

// A path CloudNativePatroni owns outright is marked so that `git check-attr`
// can explain it, but it is not guarded: upstream has no counterpart to merge.
func TestGitAttributesMarksOwnedPathsWithoutGuardingThem(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, `  - id: owned.tooling
    ownership: cnpatroni-owned
    paths: ["hack/cnpatroni/**"]
`+validRules, ""))

	var line string
	for _, candidate := range strings.Split(out, "\n") {
		if strings.HasPrefix(candidate, "hack/cnpatroni/** ") {
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("the owned path is missing from the rendered attributes:\n%s", out)
	}
	if !strings.Contains(line, "cnpatroni-boundary=cnpatroni-owned") {
		t.Errorf("line %q should carry the ownership class", line)
	}
	if strings.Contains(line, "merge=") {
		t.Errorf("line %q must not route an owned path through the merge driver", line)
	}
}

func TestGitAttributesIsGeneratedAndSaysSo(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, validRules, ""))

	if !strings.HasPrefix(out, "#") {
		t.Errorf("the rendered file must open with a comment saying it is generated:\n%s", out)
	}
	if !strings.Contains(out, "cnpatroni-upstream gitattributes") {
		t.Error("the header must name the command that regenerates the file")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("the rendered file must end with a newline")
	}
}

// Rendering must be deterministic, or the freshness check would be noise.
func TestGitAttributesIsStable(t *testing.T) {
	body := manifestBody(false, validRules, "")
	first := renderAttributes(t, body)
	second := renderAttributes(t, body)
	if first != second {
		t.Errorf("two renderings of the same manifest differ:\n%s\n---\n%s", first, second)
	}
}

// Git hands a merge driver temporary file names that are relative to the
// repository root, so a driver command wrapped in a `cd` cannot open them. The
// header must not suggest one.
func TestGitAttributesHeaderRegistersTheDriverWithoutChangingDirectory(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, validRules, ""))

	var driverLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "merge-driver") {
			driverLine = line
		}
	}
	if driverLine == "" {
		t.Fatalf("the header does not show how to register the driver:\n%s", out)
	}
	if strings.Contains(driverLine, "cd ") {
		t.Errorf("the driver command changes directory, which breaks git's relative paths: %q", driverLine)
	}
	// The placeholders are single-quoted, because git hands the whole command
	// to a shell; see TestGitAttributesHeaderQuotesTheDriverCommand.
	if !strings.Contains(driverLine, "'%O' '%A' '%B' '%L' '%P'") {
		t.Errorf("the driver command must take git's five placeholders: %q", driverLine)
	}
}

func TestGitAttributesHeaderQuotesTheDriverCommand(t *testing.T) {
	out := renderAttributes(t, manifestBody(false, validRules, ""))

	var driverLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "merge-driver") {
			driverLine = line
		}
	}
	if driverLine == "" {
		t.Fatalf("the header does not show how to register the driver:\n%s", out)
	}
	if !strings.Contains(driverLine, `"'$root/bin/cnpatroni-upstream' merge-driver`) {
		t.Errorf("the published recipe leaves the driver path unquoted: %q", driverLine)
	}
	for _, placeholder := range []string{"%O", "%A", "%B", "%L", "%P"} {
		if !strings.Contains(driverLine, "'"+placeholder+"'") {
			t.Errorf("the published recipe leaves %s unquoted: %q", placeholder, driverLine)
		}
	}
}
