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

package app

import "testing"

func clusterBody(leader string) []byte {
	return []byte(`{"members":[{"name":"pod-a","role":"leader","state":"running"},{"name":"pod-b","role":"replica","state":"streaming"},{"name":"pod-c","role":"replica","state":"streaming"}],"leader_override":"` + leader + `"}`)
}

func TestValidateClusterViews(t *testing.T) {
	views := map[string][]byte{"pod-a": clusterBody("pod-a"), "pod-b": clusterBody("pod-a"), "pod-c": clusterBody("pod-a")}
	got, err := ValidateClusterViews([]string{"pod-a", "pod-b", "pod-c"}, views, "pod-a")
	if err != nil {
		t.Fatalf("ValidateClusterViews() error = %v", err)
	}
	if got.Leader != "pod-a" || got.StreamingReplicas != 2 {
		t.Fatalf("view = %#v", got)
	}
}

func TestValidateClusterViewsRejectsDisagreementAndMissingStreaming(t *testing.T) {
	disagree := []byte(`{"members":[{"name":"pod-a","role":"replica","state":"streaming"},{"name":"pod-b","role":"leader","state":"running"},{"name":"pod-c","role":"replica","state":"streaming"}]}`)
	views := map[string][]byte{"pod-a": clusterBody("pod-a"), "pod-b": disagree, "pod-c": clusterBody("pod-a")}
	if _, err := ValidateClusterViews([]string{"pod-a", "pod-b", "pod-c"}, views, "pod-a"); err == nil {
		t.Fatal("ValidateClusterViews(disagreement) error = nil")
	}
	missing := []byte(`{"members":[{"name":"pod-a","role":"leader","state":"running"},{"name":"pod-b","role":"replica","state":"stopped"},{"name":"pod-c","role":"replica","state":"streaming"}]}`)
	views = map[string][]byte{"pod-a": missing, "pod-b": missing, "pod-c": missing}
	if _, err := ValidateClusterViews([]string{"pod-a", "pod-b", "pod-c"}, views, "pod-a"); err == nil {
		t.Fatal("ValidateClusterViews(missing streaming) error = nil")
	}
}

func TestValidateClusterViewsRejectsEndpointMismatch(t *testing.T) {
	views := map[string][]byte{"pod-a": clusterBody("pod-a"), "pod-b": clusterBody("pod-a"), "pod-c": clusterBody("pod-a")}
	if _, err := ValidateClusterViews([]string{"pod-a", "pod-b", "pod-c"}, views, "pod-b"); err == nil {
		t.Fatal("ValidateClusterViews(endpoint mismatch) error = nil")
	}
}
