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

// Command cnpatroni-upstream keeps the CloudNativePatroni fork answerable about
// upstream CloudNativePG: it configures a clone, validates the boundary
// manifest and reports the divergence that a maintainer must review before
// absorbing upstream changes.
package main

import (
	"os"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
