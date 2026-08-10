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

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsafeContext distinguishes the safety guard from ordinary setup errors.
var ErrUnsafeContext = errors.New("unsafe Kubernetes context")

// Node is the context guard's minimal, pure node view.
type Node struct {
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
}

// ValidateContext requires an explicit kind context and validates every node's
// independent provider and name evidence.
func ValidateContext(contextName string, nodes []Node) error {
	if contextName == "" {
		return fmt.Errorf("%w: --context is mandatory", ErrUnsafeContext)
	}
	if !strings.HasPrefix(contextName, "kind-") || len(contextName) == len("kind-") {
		return fmt.Errorf("%w: context %q does not match ^kind-", ErrUnsafeContext, contextName)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("%w: no nodes were returned", ErrUnsafeContext)
	}
	clusterName := strings.TrimPrefix(contextName, "kind-")
	nodePrefix := clusterName + "-"
	for _, node := range nodes {
		if !strings.HasPrefix(node.ProviderID, "kind://") {
			return fmt.Errorf("%w: node %q providerID %q does not begin kind://", ErrUnsafeContext, node.Name, node.ProviderID)
		}
		if !strings.HasPrefix(node.Name, nodePrefix) {
			return fmt.Errorf("%w: node %q does not begin %q", ErrUnsafeContext, node.Name, nodePrefix)
		}
	}
	return nil
}
