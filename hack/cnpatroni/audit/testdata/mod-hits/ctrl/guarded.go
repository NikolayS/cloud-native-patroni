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

import "example.com/hits/guard"

// GuardedOK passes its own symbol to the guard.
func GuardedOK() error { return guard.Forbidden("example.com/hits/ctrl.GuardedOK") }

// GuardedWrongLiteral passes a symbol that is not its own.
func GuardedWrongLiteral() error { return guard.Forbidden("example.com/hits/ctrl.Elsewhere") }

// GuardedMissing is classified as requiring a guard but does not call one.
func GuardedMissing() error { return nil }
