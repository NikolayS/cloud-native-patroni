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

package ctrl

import (
	"context"
	"os/exec"
)

const pgCtlBinary = "/usr/bin/" + "pg_ctl"

// ExecPgCtl uses a folded constant so the scanner must use type information.
func ExecPgCtl() *exec.Cmd { return exec.Command(pgCtlBinary, "promote") }

// ExecPgCtlContext exercises the context-aware standard library entry point.
func ExecPgCtlContext(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "pg_ctl") }

// ExecDynamic cannot be classified statically and must not be guessed at.
func ExecDynamic(binary string) *exec.Cmd { return exec.Command(binary) }

// ExecUnrelated is a constant call to another executable and must not match.
func ExecUnrelated() *exec.Cmd { return exec.Command("postgres") }
