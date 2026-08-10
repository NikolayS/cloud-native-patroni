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

// Package guard contains the host driver's pure kind-cluster safety guard.
package guard

import (
	"fmt"
	"strings"
)

// Node is the independent provider and name evidence required by the guard.
type Node struct {
	Name       string
	ProviderID string
}

// ValidateContext rejects empty, non-kind, mixed-provider, and foreign-name
// contexts before the driver can reach a fault primitive.
func ValidateContext(contextName string, nodes []Node) error {
	if !strings.HasPrefix(contextName, "kind-") || len(contextName) == len("kind-") {
		return fmt.Errorf("--context is mandatory and must match ^kind-")
	}
	if len(nodes) == 0 {
		return fmt.Errorf("context guard returned zero nodes")
	}
	cluster := strings.TrimPrefix(contextName, "kind-")
	for _, node := range nodes {
		if !strings.HasPrefix(node.ProviderID, "kind://") {
			return fmt.Errorf("node %q providerID %q does not begin kind://", node.Name, node.ProviderID)
		}
		if !strings.HasPrefix(node.Name, cluster+"-") {
			return fmt.Errorf("node %q does not begin kind cluster prefix %q", node.Name, cluster+"-")
		}
	}
	return nil
}
