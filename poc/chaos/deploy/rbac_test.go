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

package deploy

import (
	"os"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

type manifest struct {
	Kind  string `yaml:"kind"`
	Rules []struct {
		Verbs []string `yaml:"verbs"`
	} `yaml:"rules"`
}

func TestOracleRBACIsReadOnly(t *testing.T) {
	for _, name := range []string{"role.yaml", "cluster-role.yaml"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		var object manifest
		if err := yaml.Unmarshal(data, &object); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if object.Kind != "Role" && object.Kind != "ClusterRole" {
			t.Fatalf("%s kind = %q", name, object.Kind)
		}
		if len(object.Rules) == 0 {
			t.Fatalf("%s has no rules", name)
		}
		for index, rule := range object.Rules {
			if !slices.Equal(rule.Verbs, []string{"get", "list", "watch"}) {
				t.Fatalf("%s rule %d verbs = %v, want get/list/watch only", name, index, rule.Verbs)
			}
		}
	}
}
