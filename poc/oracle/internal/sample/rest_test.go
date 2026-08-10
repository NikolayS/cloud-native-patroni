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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestRESTSamplerUsesOnlyGuardedGET(t *testing.T) {
	origin := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "10.244.1.2:8008" || request.URL.Path != "/patroni" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"role":"primary"}`))}, nil
	})}
	sampler := RESTSampler{Client: client, Port: 8008, Origin: origin, Now: func() time.Time { return origin.Add(1250 * time.Millisecond) }}
	got := sampler.Sample(context.Background(), "cluster-1", "10.244.1.2", "/patroni")
	if !got.OK || got.StatusCode != 200 || got.MonoNS != int64(1250*time.Millisecond) || got.Raw != `{"role":"primary"}` {
		t.Fatalf("Sample() = %#v", got)
	}
}

func TestRESTSamplerRejectsHostnameAndUnlistedPath(t *testing.T) {
	sampler := RESTSampler{Client: http.DefaultClient, Port: 8008, Origin: time.Now(), Now: time.Now}
	for _, test := range []struct{ ip, path string }{{"cluster-1.database.svc", "/patroni"}, {"10.244.1.2", "/switchover"}} {
		got := sampler.Sample(context.Background(), "cluster-1", test.ip, test.path)
		if got.OK || got.Error == "" {
			t.Fatalf("Sample(%q, %q) = %#v, want guarded failure", test.ip, test.path, got)
		}
	}
}

func TestSchemaTrackerReturnsSortedObservedFields(t *testing.T) {
	tracker := NewSchemaTracker()
	tracker.Observe("/patroni", []byte(`{"z":1,"state":"running","patroni":{"version":"4.1.4"}}`))
	tracker.Observe("/patroni", []byte(`{"role":"primary","state":"running"}`))
	got := tracker.Snapshot()
	want := []string{"patroni", "role", "state", "z"}
	if strings.Join(got["/patroni"], ",") != strings.Join(want, ",") {
		t.Fatalf("fields = %v, want %v", got["/patroni"], want)
	}
}
