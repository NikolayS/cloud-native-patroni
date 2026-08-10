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

package inject

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudnative-pg/cloudnative-pg/poc/chaos/internal/guard"
)

// ContextGuard executes the live node-list check. Fault primitives call Verify
// again immediately before their state-changing command.
type ContextGuard struct {
	Context  string
	Commands *Recorder
}

// Verify lists nodes with an explicit context and applies the pure guard.
func (g ContextGuard) Verify(ctx context.Context) error {
	if g.Commands == nil {
		return fmt.Errorf("context guard command recorder is nil")
	}
	result := g.Commands.Run(ctx, []string{"kubectl", "--context", g.Context, "get", "nodes", "-o", "json"}, nil)
	if result.ExitCode != 0 {
		return fmt.Errorf("context guard node list failed with exit %d: %s", result.ExitCode, result.Stderr)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				ProviderID string `json:"providerID"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &list); err != nil {
		return fmt.Errorf("decode context guard node list: %w", err)
	}
	nodes := make([]guard.Node, 0, len(list.Items))
	for _, item := range list.Items {
		nodes = append(nodes, guard.Node{Name: item.Metadata.Name, ProviderID: item.Spec.ProviderID})
	}
	return guard.ValidateContext(g.Context, nodes)
}
