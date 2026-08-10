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
	"net"
	"net/url"
	"strings"
)

// ValidateConnectionString proves that the target is a non-loopback literal
// Pod IP and that libpq target filtering cannot hide read-only nodes.
func ValidateConnectionString(connectionString string) (net.IP, error) {
	parsed, err := url.Parse(connectionString)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return nil, fmt.Errorf("connection scheme %q is not postgres", parsed.Scheme)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("database host %q is not a literal IP address", host)
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return nil, fmt.Errorf("database host %q is not a Pod-routable literal IP address", host)
	}
	target := parsed.Query().Get("target_session_attrs")
	if target != "" && !strings.EqualFold(target, "any") {
		return nil, fmt.Errorf("target_session_attrs %q would filter measured nodes", target)
	}
	return ip, nil
}
