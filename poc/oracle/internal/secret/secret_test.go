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

package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	tests := []struct {
		name    string
		path    string
		env     string
		lookup  func(string) (string, bool)
		want    string
		wantErr bool
	}{
		{name: "file", path: path, want: "file-secret"},
		{name: "environment", env: "CNPATRONI_CHAOS_PASSWORD", lookup: func(string) (string, bool) { return "env-secret", true }, want: "env-secret"},
		{name: "both", path: path, env: "PASSWORD", lookup: func(string) (string, bool) { return "env-secret", true }, wantErr: true},
		{name: "neither", wantErr: true},
		{name: "empty environment", env: "PASSWORD", lookup: func(string) (string, bool) { return "", true }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := tt.lookup
			if lookup == nil {
				lookup = func(string) (string, bool) { return "", false }
			}
			got, err := Load(tt.path, tt.env, lookup)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Load() = %q, want %q", got, tt.want)
			}
		})
	}
}
