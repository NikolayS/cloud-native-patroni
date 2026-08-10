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

// Package ctrl exercises every scanner engine.
package ctrl

import (
	"context"
	"net/http"

	"example.com/hits/api"
	"example.com/hits/pg"
)

// signalFile is a literal hit for the recovery-topology rule.
const signalFile = "standby.signal"

// Promote is a forbidden promotion call site.
func Promote(i *pg.Instance) error { return i.PromoteAndWait() }

// StopBoth calls Shutdown on the guarded type and on an unrelated type with the
// same method name. Only the first is a hit.
func StopBoth(i *pg.Instance, s *http.Server) {
	_ = i.Shutdown()
	_ = s.Shutdown(context.Background())
}

// SetPrimary writes the desired-primary field.
func SetPrimary(st *api.ClusterStatus) {
	st.TargetPrimary = "cluster-1"
}

// ReadPrimary only reads the observed-primary field.
func ReadPrimary(st *api.ClusterStatus) string {
	return st.CurrentPrimary
}

// Signal returns the literal.
func Signal() string { return signalFile }

// Commented mentions PromoteAndWait and standby.signal only in comments.
//
//	i.PromoteAndWait()
//	"standby.signal"
func Commented() {
	/* i.PromoteAndWait() and "standby.signal" appear here too */
}
