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

package guard

import "testing"

func TestValidateConnectionString(t *testing.T) {
	tests := []struct {
		name    string
		conn    string
		wantIP  string
		wantErr bool
	}{
		{name: "IPv4 literal", conn: "postgres://user@10.244.2.7:5432/postgres?sslmode=disable", wantIP: "10.244.2.7"},
		{name: "IPv6 literal", conn: "postgres://user@[fd00::7]:5432/postgres?target_session_attrs=any", wantIP: "fd00::7"},
		{name: "service", conn: "postgres://user@cluster-rw:5432/postgres", wantErr: true},
		{name: "dns name", conn: "postgres://user@pod.namespace.svc:5432/postgres", wantErr: true},
		{name: "localhost", conn: "postgres://user@localhost:5432/postgres", wantErr: true},
		{name: "loopback literal", conn: "postgres://user@127.0.0.1:5432/postgres", wantErr: true},
		{name: "read write target filtering", conn: "postgres://user@10.244.2.7/postgres?target_session_attrs=read-write", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateConnectionString(tt.conn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConnectionString() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.wantIP {
				t.Fatalf("ValidateConnectionString() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}
