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

func TestValidateContext(t *testing.T) {
	tests := []struct {
		name    string
		context string
		nodes   []Node
		wantErr bool
	}{
		{name: "empty", wantErr: true},
		{name: "GKE shaped", context: "gke_project_region_cluster", wantErr: true},
		{name: "foreign provider", context: "kind-demo", nodes: []Node{{Name: "demo-worker", ProviderID: "gce://project/zone/node"}}, wantErr: true},
		{name: "foreign name", context: "kind-demo", nodes: []Node{{Name: "prod-worker", ProviderID: "kind://docker/demo/prod-worker"}}, wantErr: true},
		{name: "happy", context: "kind-demo", nodes: []Node{{Name: "demo-control-plane", ProviderID: "kind://docker/demo/demo-control-plane"}, {Name: "demo-worker", ProviderID: "kind://docker/demo/demo-worker"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateContext(tt.context, tt.nodes); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateContext() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
