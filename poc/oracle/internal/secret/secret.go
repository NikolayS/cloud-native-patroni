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

// Package secret loads passwords without admitting them to command-line
// arguments or logs.
package secret

import (
	"fmt"
	"os"
	"strings"
)

// LookupEnv matches os.LookupEnv and is injectable for tests.
type LookupEnv func(string) (string, bool)

// Load reads exactly one configured password source.
func Load(path, envName string, lookup LookupEnv) (string, error) {
	if path != "" && envName != "" {
		return "", fmt.Errorf("password file and environment sources are mutually exclusive")
	}
	if path == "" && envName == "" {
		return "", fmt.Errorf("password source is required")
	}
	var value string
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		value = strings.TrimRight(string(data), "\r\n")
	} else {
		var ok bool
		value, ok = lookup(envName)
		if !ok {
			return "", fmt.Errorf("password environment variable %q is unset", envName)
		}
	}
	if value == "" {
		return "", fmt.Errorf("password is empty")
	}
	return value, nil
}
