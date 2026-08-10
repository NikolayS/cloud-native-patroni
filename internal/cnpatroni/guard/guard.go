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

// Package guard fails the PostgreSQL lifecycle operations that Patroni owns in
// CloudNativePatroni.
//
// Patroni is the sole authority for PostgreSQL start, stop, promotion, demotion,
// replication topology, and former-primary recovery (specification sections 4.1
// and 7.4). The functions that implement those operations in CloudNativePG are
// disabled by calling Forbidden and returning the error it produces.
//
// The guard is unconditional. There is no build tag, environment variable, or
// operator setting that disarms it, because CloudNativePatroni has no supported
// mode in which the CloudNativePG high-availability paths may run.
//
// # Status in milestone M0
//
// Nothing in this repository calls this package yet, and no CloudNativePG code
// has been modified. The call sites land in M1, together with the
// process-lifecycle surgery, and only after ADR-002 is accepted (specification
// section 21). The package ships first so that the authority audit in
// hack/cnpatroni/audit can verify each classification marked "guard: required"
// against a call site that already exists, rather than the audit and the guard
// arriving in the same change with nothing checking either.
//
// The intended M1 call site is one line per forbidden primitive:
//
//	func (instance *Instance) PromoteAndWait(_ context.Context) error {
//		return guard.Forbidden(
//			"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres.(*Instance).PromoteAndWait")
//	}
package guard

import (
	"errors"
	"fmt"
	"testing"
)

// ErrForbidden is wrapped by every error Forbidden returns, so that callers and
// tests can match it with errors.Is.
var ErrForbidden = errors.New(
	"forbidden in CloudNativePatroni: Patroni is the sole PostgreSQL lifecycle authority")

// panicOnForbidden makes a reached guard impossible to swallow while testing.
// It is a variable rather than a constant only so that this package's own tests
// can exercise both behaviours.
var panicOnForbidden = testing.Testing()

// Forbidden reports that op was reached and returns an error wrapping
// ErrForbidden. Callers must return that error; they must not ignore it.
//
// op is the fully qualified symbol of the calling function, written exactly as
// it appears in hack/cnpatroni/audit/policy/authority-classification.yaml, for
// example
// "github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres.(*Instance).PromoteAndWait".
// The literal is passed explicitly rather than derived with runtime.Caller
// because the authority audit has to verify the wiring without executing
// anything: a string literal is visible to go/ast, a stack frame is not. The
// audit cross-checks that the literal equals the enclosing symbol, which removes
// the typo risk that would otherwise argue for deriving it.
//
// Under go test Forbidden panics with the same error, so that an accidental call
// in a unit test fails with a stack trace instead of flowing into an error branch
// that the test happens to tolerate. In a production build it only returns the
// error: panicking would crash an operator process that manages every cluster in
// its namespace over a defect in one code path.
func Forbidden(op string) error {
	err := newForbidden(op)
	if panicOnForbidden {
		panic(err)
	}
	return err
}

func newForbidden(op string) error {
	if op == "" {
		op = "<unnamed operation: guard.Forbidden called with an empty op>"
	}
	return fmt.Errorf(
		"%w: %s must not run; see hack/cnpatroni/audit/policy/authority-classification.yaml"+
			" and specification section 7.4",
		ErrForbidden, op)
}
