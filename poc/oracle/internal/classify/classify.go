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

// Package classify maps protocol-aware pgx errors to the closed outcome set.
package classify

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

var refusedStates = map[string]struct{}{
	"25006": {},
	"57P03": {},
	"57P01": {},
	"57P02": {},
	"08006": {},
	"08001": {},
	"08003": {},
	"08004": {},
}

// Policy carries evidence needed for conditional classifications.
type Policy struct {
	ConnectionBudgetExceeded bool
}

// Outcome classifies an attempt without conditional connection-budget
// evidence. A 53300 is therefore a harness misconfiguration by default.
func Outcome(dispatched bool, err error) model.Outcome {
	return OutcomeWithPolicy(dispatched, err, Policy{})
}

// OutcomeWithPolicy applies the protocol-level classification lattice.
func OutcomeWithPolicy(dispatched bool, err error, policy Policy) model.Outcome {
	if err == nil {
		return model.Committed
	}
	if !dispatched {
		return model.Unreachable
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if _, ok := refusedStates[pgErr.Code]; ok {
			return model.Refused
		}
		if pgErr.Code == "53300" && policy.ConnectionBudgetExceeded {
			return model.Refused
		}
		return model.Misconfigured
	}
	return model.InDoubt
}

// SQLState returns a server SQLSTATE or the empty string when the server did
// not provide one.
func SQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
