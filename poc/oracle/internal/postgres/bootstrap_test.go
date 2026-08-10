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
	"errors"
	"testing"
)

type fakeSanitizer struct {
	result string
	err    error
	input  string
}

func (f *fakeSanitizer) EscapeString(input string) (string, error) {
	f.input = input
	return f.result, f.err
}

func TestBootstrapRoleUsesLiteralSanitizer(t *testing.T) {
	sanitizer := &fakeSanitizer{result: `a''b`}
	got, err := BootstrapRoleSQL(sanitizer, "a'b")
	if err != nil {
		t.Fatalf("BootstrapRoleSQL() error = %v", err)
	}
	if got != `create role cnpatroni_chaos login password 'a''b' noinherit;` || sanitizer.input != "a'b" {
		t.Fatalf("result=%q input=%q", got, sanitizer.input)
	}
}

func TestBootstrapRoleDoesNotReturnPartialSQLOnSanitizerError(t *testing.T) {
	got, err := BootstrapRoleSQL(&fakeSanitizer{result: "unsafe", err: errors.New("bad encoding")}, "secret")
	if err == nil || got != "" {
		t.Fatalf("BootstrapRoleSQL() = %q, %v; want empty and error", got, err)
	}
}
