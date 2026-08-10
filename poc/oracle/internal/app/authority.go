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

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/kube"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/sample"
)

// AuthorityPreflight is the agreed read-only cluster view.
type AuthorityPreflight struct {
	Leader            string `json:"leader"`
	StreamingReplicas int    `json:"streaming_replicas"`
}

// ValidateClusterViews requires every direct /cluster view and the Endpoints
// annotation to agree on one leader and exactly two streaming replicas.
func ValidateClusterViews(expected []string, rawViews map[string][]byte, endpointLeader string) (AuthorityPreflight, error) {
	expectedSorted := append([]string(nil), expected...)
	sort.Strings(expectedSorted)
	var agreedLeader string
	streaming := -1
	for _, observer := range expected {
		raw, ok := rawViews[observer]
		if !ok {
			return AuthorityPreflight{}, fmt.Errorf("Pod %q has no /cluster view", observer)
		}
		var view struct {
			Members []struct {
				Name  string `json:"name"`
				Role  string `json:"role"`
				State string `json:"state"`
			} `json:"members"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return AuthorityPreflight{}, fmt.Errorf("decode /cluster from %q: %w", observer, err)
		}
		var names []string
		leader, replicas := "", 0
		for _, member := range view.Members {
			names = append(names, member.Name)
			if member.Role == "leader" || member.Role == "primary" || member.Role == "standby_leader" {
				if leader != "" {
					return AuthorityPreflight{}, fmt.Errorf("Pod %q sees multiple leaders", observer)
				}
				leader = member.Name
			}
			if member.Role == "replica" && member.State == "streaming" {
				replicas++
			}
		}
		sort.Strings(names)
		if !slices.Equal(names, expectedSorted) {
			return AuthorityPreflight{}, fmt.Errorf("Pod %q /cluster members=%v, want %v", observer, names, expectedSorted)
		}
		if leader == "" {
			return AuthorityPreflight{}, fmt.Errorf("Pod %q sees no leader", observer)
		}
		if agreedLeader != "" && agreedLeader != leader {
			return AuthorityPreflight{}, fmt.Errorf("/cluster leader disagreement: %q and %q", agreedLeader, leader)
		}
		agreedLeader = leader
		if streaming >= 0 && streaming != replicas {
			return AuthorityPreflight{}, fmt.Errorf("/cluster streaming replica count disagreement")
		}
		streaming = replicas
	}
	if agreedLeader != endpointLeader {
		return AuthorityPreflight{}, fmt.Errorf("/cluster leader %q disagrees with Endpoints leader %q", agreedLeader, endpointLeader)
	}
	if streaming != len(expected)-1 {
		return AuthorityPreflight{}, fmt.Errorf("streaming replicas=%d, want %d", streaming, len(expected)-1)
	}
	return AuthorityPreflight{Leader: agreedLeader, StreamingReplicas: streaming}, nil
}

func validateAuthorityLive(ctx context.Context, config Config, inventory []kube.PodInventory, client *kube.Client) (AuthorityPreflight, error) {
	views := make(map[string][]byte, len(inventory))
	for _, pod := range inventory {
		record := (sample.RESTSampler{Port: config.PatroniPort, Origin: time.Now(), Now: time.Now}).Sample(ctx, pod.Name, pod.IP, "/cluster")
		if !record.OK {
			return AuthorityPreflight{}, fmt.Errorf("Patroni /cluster on %q: %s", pod.Name, record.Error)
		}
		views[pod.Name] = []byte(record.Raw)
	}
	endpoint, err := client.EndpointSnapshot(ctx, config.Namespace, config.Scope)
	if err != nil {
		return AuthorityPreflight{}, err
	}
	ok, endpointLeader := dcsLeader(endpoint.Annotations)
	if !ok {
		return AuthorityPreflight{}, fmt.Errorf("Endpoints leader annotation is missing or unrecognised")
	}
	return ValidateClusterViews(config.ExpectedNodes, views, endpointLeader)
}
