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
	"strings"
)

// MergeDriverName is the git merge driver that always reports a conflict on a
// boundary path. recon-06 section 5.1 measured git auto-merging an 87-line
// upstream change to pkg/management/postgres/instance.go cleanly and silently,
// so git's own conflict signal is not enough to make a boundary change visible.
const MergeDriverName = "cnpatroni-boundary"

// AttributeName is the git attribute that records a path's ownership class, so
// that `git check-attr` can explain why a merge stopped.
const AttributeName = "cnpatroni-boundary"

// gitAttributesHeader opens the generated file. It has to say the file is
// generated, because a hand edit would be silently overwritten.
const gitAttributesHeader = `# Generated from hack/cnpatroni/upstream/boundary.yaml. Do not edit by hand.
#
# Regenerate with:
#   (cd hack/cnpatroni/upstream && go run ./cmd/cnpatroni-upstream gitattributes)
#
# The merge driver named here has to be registered once per clone, because git
# refuses to take a driver definition from a tracked file:
#   root=$(git rev-parse --show-toplevel)
#   go build -C hack/cnpatroni/upstream \
#     -o "$root/bin/cnpatroni-upstream" ./cmd/cnpatroni-upstream
#   git config merge.` + MergeDriverName + `.name "CloudNativePatroni boundary guard"
#   git config merge.` + MergeDriverName + `.driver \
#     "$root/bin/cnpatroni-upstream merge-driver %O %A %B %L %P"
#
# The driver command must be an absolute path and must not change directory:
# git substitutes %O %A %B with temporary file names that are relative to the
# repository root, so a driver that cds elsewhere cannot open them.
#
# Without that registration git falls back to its ordinary three-way merge and
# the guard is inert, which is why it is one of three independent mechanisms
# rather than the only one.
`

// GitAttributes renders the .gitattributes content the manifest implies.
//
// Every path whose ownership obliges a human to read the upstream hunks is
// routed through the always-conflict merge driver. Paths CloudNativePatroni
// owns outright are marked but not guarded: upstream has no counterpart to
// merge, so a conflict there would be a name collision, which the divergence
// report already reports. The catch-all rule is never rendered, because an
// attribute line for `**` would route the whole repository through the driver.
func GitAttributes(m *Manifest) string {
	var b strings.Builder
	b.WriteString(gitAttributesHeader)

	for i := range m.Rules {
		rule := &m.Rules[i]
		if rule.IsCatchAll() || rule.Ownership == OwnershipUpstreamUntouched {
			continue
		}

		b.WriteString("\n# ")
		b.WriteString(rule.ID)
		b.WriteString("\n")
		for _, pattern := range rule.Paths {
			b.WriteString(pattern)
			b.WriteString(" ")
			b.WriteString(AttributeName)
			b.WriteString("=")
			b.WriteString(string(rule.Ownership))
			if rule.Ownership.NeedsReview() {
				b.WriteString(" merge=")
				b.WriteString(MergeDriverName)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
