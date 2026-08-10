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
	"fmt"
	"net/http"
)

var patroniPaths = []string{
	"/patroni",
	"/primary",
	"/replica",
	"/liveness",
	"/readiness",
	"/health",
	"/cluster",
	"/config",
	"/metrics",
	"/failsafe",
}

var patroniPathSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(patroniPaths))
	for _, path := range patroniPaths {
		result[path] = struct{}{}
	}
	return result
}()

// PatroniRequest is the only type from which the REST sampler constructs an
// outbound Patroni request.
type PatroniRequest struct {
	Method string
	Path   string
}

// PatroniPaths returns a defensive copy of the reviewed path allowlist.
func PatroniPaths() []string {
	return append([]string(nil), patroniPaths...)
}

// NewPatroniRequest rejects every method except GET and every unreviewed path.
func NewPatroniRequest(method, path string) (PatroniRequest, error) {
	if method != http.MethodGet {
		return PatroniRequest{}, fmt.Errorf("Patroni method %q is not allowed", method)
	}
	if _, ok := patroniPathSet[path]; !ok {
		return PatroniRequest{}, fmt.Errorf("Patroni path %q is not allowed", path)
	}
	return PatroniRequest{Method: method, Path: path}, nil
}
