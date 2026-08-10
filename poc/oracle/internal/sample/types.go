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

// Package sample records raw observations and extracts conservative typed
// views without inventing false values for failed or unknown fields.
package sample

import (
	"encoding/json"
	"fmt"
	"time"
)

// Record is the common envelope for every independent source.
type Record struct {
	TakenAt        time.Time `json:"taken_at"`
	MonoNS         int64     `json:"mono_ns"`
	Source         string    `json:"source"`
	Node           string    `json:"node"`
	OK             bool      `json:"ok"`
	Latency        float64   `json:"latency_ms"`
	Error          string    `json:"error,omitempty"`
	Raw            string    `json:"raw"`
	StaleCandidate bool      `json:"stale_candidate,omitempty"`
}

// PatroniView is a conservative view over a raw REST response.
type PatroniView struct {
	OK        bool
	Error     string
	Raw       json.RawMessage
	Fields    map[string]json.RawMessage
	State     string
	Role      string
	RoleKnown bool
	Timeline  *int64
}

// ParsePatroni retains every raw field and extracts only recognised values of
// the expected type.
func ParsePatroni(raw []byte) PatroniView {
	view := PatroniView{Raw: append(json.RawMessage(nil), raw...)}
	if err := json.Unmarshal(raw, &view.Fields); err != nil {
		view.Error = fmt.Sprintf("decode Patroni JSON: %v", err)
		return view
	}
	view.OK = true
	if value, ok := view.Fields["state"]; ok {
		_ = json.Unmarshal(value, &view.State)
	}
	if value, ok := view.Fields["role"]; ok && json.Unmarshal(value, &view.Role) == nil && view.Role != "" {
		view.RoleKnown = true
	}
	if value, ok := view.Fields["timeline"]; ok {
		var timeline int64
		if json.Unmarshal(value, &timeline) == nil {
			view.Timeline = &timeline
		}
	}
	return view
}
