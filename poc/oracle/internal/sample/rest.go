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

package sample

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
)

const sourceTimeout = 500 * time.Millisecond

// RESTRecord adds HTTP evidence to the common sample envelope.
type RESTRecord struct {
	Record
	Path       string `json:"path"`
	StatusCode int    `json:"status_code,omitempty"`
}

// RESTSampler probes a Patroni endpoint directly through a literal Pod IP.
type RESTSampler struct {
	Client *http.Client
	Port   int
	Origin time.Time
	Now    func() time.Time
}

// Sample performs one guarded GET with an independent 500 ms deadline.
func (s RESTSampler) Sample(ctx context.Context, node, podIP, path string) RESTRecord {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	record := RESTRecord{Record: Record{TakenAt: started.UTC(), MonoNS: started.Sub(s.Origin).Nanoseconds(), Source: "patroni", Node: node}, Path: path}
	ip := net.ParseIP(podIP)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		record.Error = fmt.Sprintf("Patroni host %q is not a Pod-routable literal IP", podIP)
		return record
	}
	guarded, err := guard.NewPatroniRequest(http.MethodGet, path)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), strconv.Itoa(s.Port)), Path: guarded.Path}
	requestCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, guarded.Method, endpoint.String(), nil)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	client := s.Client
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	record.Latency = float64(now().Sub(started).Microseconds()) / 1000
	if err != nil {
		record.Error = err.Error()
		return record
	}
	defer response.Body.Close()
	record.StatusCode = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	record.Raw = string(body)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		record.Error = fmt.Sprintf("Patroni returned HTTP %d", response.StatusCode)
		return record
	}
	record.OK = true
	return record
}

// SchemaTracker records the empirically observed top-level fields per path.
type SchemaTracker struct {
	mu     sync.Mutex
	fields map[string]map[string]struct{}
}

// NewSchemaTracker initializes an observed-schema tracker.
func NewSchemaTracker() *SchemaTracker {
	return &SchemaTracker{fields: make(map[string]map[string]struct{})}
}

// Observe merges fields only when a response is a JSON object.
func (t *SchemaTracker) Observe(path string, raw []byte) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fields[path] == nil {
		t.fields[path] = make(map[string]struct{})
	}
	for field := range object {
		t.fields[path][field] = struct{}{}
	}
}

// Snapshot returns stable sorted field names for the bundle schema artifact.
func (t *SchemaTracker) Snapshot() map[string][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string][]string, len(t.fields))
	for path, fields := range t.fields {
		for field := range fields {
			result[path] = append(result[path], field)
		}
		sort.Strings(result[path])
	}
	return result
}
