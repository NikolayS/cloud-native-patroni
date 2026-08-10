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

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type memorySink struct{ marks []Mark }

func (s *memorySink) AppendMark(mark Mark) error {
	s.marks = append(s.marks, mark)
	return nil
}

func TestMarkUsesOracleClock(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC), func() time.Time {
		return time.Date(2026, 8, 9, 12, 0, 1, 250, time.UTC)
	}, sink)
	body, _ := json.Marshal(map[string]string{"label": "primary-kill", "phase": "before"})
	request := httptest.NewRequest(http.MethodPost, "/mark", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", response.Code, response.Body.String())
	}
	if len(sink.marks) != 1 || sink.marks[0].MonoNS != int64(time.Second)+250 || sink.marks[0].Label != "primary-kill" || sink.marks[0].Phase != "before" {
		t.Fatalf("marks = %#v", sink.marks)
	}
}

func TestMarkRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{name: "wrong method", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "bad JSON", method: http.MethodPost, body: `{`, want: http.StatusBadRequest},
		{name: "empty label", method: http.MethodPost, body: `{"phase":"before"}`, want: http.StatusBadRequest},
		{name: "invalid phase", method: http.MethodPost, body: `{"label":"fault","phase":"during"}`, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(time.Now(), time.Now, &memorySink{})
			request := httptest.NewRequest(tt.method, "/mark", bytes.NewBufferString(tt.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

type fakeProber struct {
	host  string
	ports []int
}

type observationSink struct {
	memorySink
	observations []ContainerObservation
}

func (s *observationSink) AppendContainerObservation(observation ContainerObservation) error {
	s.observations = append(s.observations, observation)
	return nil
}

func (p *fakeProber) Probe(_ context.Context, host string, ports []int) map[int]string {
	p.host, p.ports = host, append([]int(nil), ports...)
	return map[int]string{5432: "reachable", 8008: "reachable"}
}

func TestProbeAcceptsOnlyLiteralPodIPAndMeasuredPorts(t *testing.T) {
	prober := &fakeProber{}
	handler := NewHandlerWithProber(time.Now(), time.Now, &memorySink{}, prober)
	request := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewBufferString(`{"host":"10.244.3.2","ports":[5432,8008]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || prober.host != "10.244.3.2" || len(prober.ports) != 2 || prober.ports[0] != 5432 || prober.ports[1] != 8008 {
		t.Fatalf("status=%d prober=%#v body=%q", response.Code, prober, response.Body.String())
	}
	for _, body := range []string{`{"host":"cluster-rw","ports":[5432]}`, `{"host":"127.0.0.1","ports":[5432]}`, `{"host":"10.244.3.2","ports":[22]}`} {
		request = httptest.NewRequest(http.MethodPost, "/probe", bytes.NewBufferString(body))
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d, want 400", body, response.Code)
		}
	}
}

func TestContainerObservationGetsOracleTimestamp(t *testing.T) {
	origin := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	sink := &observationSink{}
	handler := NewHandlerWithProber(origin, func() time.Time { return origin.Add(5 * time.Second) }, sink, &fakeProber{})
	body := `{"node":"cluster-1","source":"docker","ok":true,"pid1_comm":"patroni","pid1_cmdline":"patroni /etc/patroni.yml","zombie_count":0,"cgroup":"/kubepods/a","raw":"evidence"}`
	request := httptest.NewRequest(http.MethodPost, "/observe/container", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(sink.observations) != 1 || sink.observations[0].MonoNS != int64(5*time.Second) || sink.observations[0].PID1Comm != "patroni" {
		t.Fatalf("status=%d observations=%#v", response.Code, sink.observations)
	}
}

func TestContainerObservationRejectsUnknownSourceAndMissingComm(t *testing.T) {
	handler := NewHandlerWithProber(time.Now(), time.Now, &observationSink{}, &fakeProber{})
	for _, body := range []string{
		`{"node":"cluster-1","source":"ssh","ok":true,"pid1_comm":"patroni"}`,
		`{"node":"cluster-1","source":"docker","ok":true}`,
		`{"node":"../escape","source":"docker","ok":false,"error":"bad"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/observe/container", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d, want 400", body, response.Code)
		}
	}
}
