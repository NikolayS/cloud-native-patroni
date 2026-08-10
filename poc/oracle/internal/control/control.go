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

// Package control brackets host faults on the oracle's monotonic clock.
package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Mark is one side of a fault interval on the oracle clock.
type Mark struct {
	TakenAt time.Time `json:"taken_at"`
	MonoNS  int64     `json:"mono_ns"`
	Label   string    `json:"label"`
	Phase   string    `json:"phase"`
}

// MarkSink incrementally persists marks.
type MarkSink interface {
	AppendMark(Mark) error
}

// ContainerObservation is read-only PID namespace evidence timestamped by the
// oracle after it arrives through the control port.
type ContainerObservation struct {
	TakenAt     time.Time `json:"taken_at"`
	MonoNS      int64     `json:"mono_ns"`
	Node        string    `json:"node"`
	Source      string    `json:"source"`
	OK          bool      `json:"ok"`
	PID1Comm    string    `json:"pid1_comm,omitempty"`
	PID1Cmdline string    `json:"pid1_cmdline,omitempty"`
	ZombieCount int       `json:"zombie_count,omitempty"`
	Cgroup      string    `json:"cgroup,omitempty"`
	Raw         string    `json:"raw,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// ObservationSink persists host-assisted read-only container evidence.
type ObservationSink interface {
	AppendContainerObservation(ContainerObservation) error
}

var safeNodeName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// NewHandler constructs the control handler. It exposes only POST /mark;
// recording a marker mutates no Kubernetes, Patroni, or Postgres state.
func NewHandler(origin time.Time, now func() time.Time, sink MarkSink) http.Handler {
	return NewHandlerWithProber(origin, now, sink, NetworkProber{})
}

// Prober performs a direct TCP reachability check from the oracle Pod.
type Prober interface {
	Probe(context.Context, string, []int) map[int]string
}

// NetworkProber is the production bounded TCP adapter.
type NetworkProber struct{}

// Probe attempts each port independently with a 400 ms timeout.
func (NetworkProber) Probe(ctx context.Context, host string, ports []int) map[int]string {
	result := make(map[int]string, len(ports))
	dialer := net.Dialer{Timeout: 400 * time.Millisecond}
	for _, port := range ports {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			result[port] = err.Error()
			continue
		}
		_ = conn.Close()
		result[port] = "reachable"
	}
	return result
}

// NewHandlerWithProber injects reachability IO for tests.
func NewHandlerWithProber(origin time.Time, now func() time.Time, sink MarkSink, prober Prober) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mark", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 4096)
		var body struct {
			Label string `json:"label"`
			Phase string `json:"phase"`
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid mark body", http.StatusBadRequest)
			return
		}
		body.Label = strings.TrimSpace(body.Label)
		if body.Label == "" || (body.Phase != "before" && body.Phase != "after") {
			http.Error(writer, "label and before/after phase are required", http.StatusBadRequest)
			return
		}
		takenAt := now()
		mark := Mark{TakenAt: takenAt.UTC(), MonoNS: takenAt.Sub(origin).Nanoseconds(), Label: body.Label, Phase: body.Phase}
		if err := sink.AppendMark(mark); err != nil {
			http.Error(writer, fmt.Sprintf("persist mark: %v", err), http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/probe", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 4096)
		var body struct {
			Host  string `json:"host"`
			Ports []int  `json:"ports"`
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid probe body", http.StatusBadRequest)
			return
		}
		ip := net.ParseIP(body.Host)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || len(body.Ports) == 0 || len(body.Ports) > 2 {
			http.Error(writer, "probe requires a Pod-routable literal IP and measured ports", http.StatusBadRequest)
			return
		}
		for _, port := range body.Ports {
			if !slices.Contains([]int{5432, 8008}, port) {
				http.Error(writer, "probe port is outside the measured allowlist", http.StatusBadRequest)
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"host": ip.String(), "results": prober.Probe(request.Context(), ip.String(), body.Ports)})
	})
	mux.HandleFunc("/observe/container", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		observer, ok := sink.(ObservationSink)
		if !ok {
			http.Error(writer, "container observation sink unavailable", http.StatusServiceUnavailable)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
		var observation ContainerObservation
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&observation); err != nil {
			http.Error(writer, "invalid container observation", http.StatusBadRequest)
			return
		}
		if len(observation.Node) > 253 || !safeNodeName.MatchString(observation.Node) || (observation.Source != "docker" && observation.Source != "kubectl") || (observation.OK && observation.PID1Comm == "") {
			http.Error(writer, "invalid node, source, or PID 1 evidence", http.StatusBadRequest)
			return
		}
		takenAt := now()
		observation.TakenAt, observation.MonoNS = takenAt.UTC(), takenAt.Sub(origin).Nanoseconds()
		if err := observer.AppendContainerObservation(observation); err != nil {
			http.Error(writer, "persist container observation", http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	return mux
}
