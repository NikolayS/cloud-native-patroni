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

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
)

// BootstrapRoleTemplateSQL is the reviewed, elided dynamic-SQL exemption.
const BootstrapRoleTemplateSQL = guard.BootstrapRoleTemplate

// LiteralSanitizer is implemented by pgconn.PgConn.EscapeString.
type LiteralSanitizer interface {
	EscapeString(string) (string, error)
}

// BootstrapRoleSQL uses pgx's SQL literal sanitizer, never fmt interpolation.
// Callers must not log the returned statement.
func BootstrapRoleSQL(sanitizer LiteralSanitizer, password string) (string, error) {
	escaped, err := sanitizer.EscapeString(password)
	if err != nil {
		return "", fmt.Errorf("quote bootstrap role password: %w", err)
	}
	return strings.Replace(BootstrapRoleTemplateSQL, "$1", "'"+escaped+"'", 1), nil
}

// Bootstrap installs the scratch role, schema, table, grants, and checkpoint
// introspection grant. The interpolated role statement is intentionally never
// returned to a logger.
func Bootstrap(ctx context.Context, conn *pgx.Conn, password string) error {
	roleSQL, err := BootstrapRoleSQL(conn.PgConn(), password)
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, roleSQL, pgx.QueryExecModeExec); err != nil {
		return fmt.Errorf("execute elided bootstrap role template: %w", err)
	}
	for _, statement := range []string{
		guard.BootstrapSchema,
		guard.BootstrapTable,
		guard.BootstrapGrantSchema,
		guard.BootstrapGrantTable,
		guard.BootstrapGrantControl,
	} {
		if err := guard.ValidateSQL(statement); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, statement, pgx.QueryExecModeExec); err != nil {
			return fmt.Errorf("execute reviewed bootstrap statement: %w", err)
		}
	}
	return nil
}
