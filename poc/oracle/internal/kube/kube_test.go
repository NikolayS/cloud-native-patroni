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

package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func kindNode(name, provider string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: corev1.NodeSpec{ProviderID: provider}}
}

func TestVerifyContext(t *testing.T) {
	tests := []struct {
		name    string
		context string
		objects []runtime.Object
		wantErr bool
	}{
		{name: "happy", context: "kind-demo", objects: []runtime.Object{kindNode("demo-control-plane", "kind://docker/demo/demo-control-plane"), kindNode("demo-worker", "kind://docker/demo/demo-worker")}},
		{name: "foreign provider", context: "kind-demo", objects: []runtime.Object{kindNode("demo-worker", "gce://project/zone/node")}, wantErr: true},
		{name: "empty context", context: "", objects: []runtime.Object{kindNode("demo-worker", "kind://docker/demo/demo-worker")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(fake.NewSimpleClientset(tt.objects...))
			err := client.VerifyContext(context.Background(), tt.context)
			if (err != nil) != tt.wantErr {
				t.Fatalf("VerifyContext() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func readyPod(name, ip, node string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "database", Labels: map[string]string{"cnpatroni.io/role": "replica"}},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "patroni"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
	}
}

func TestExpectedPodInventory(t *testing.T) {
	objects := []runtime.Object{
		readyPod("cluster-1", "10.244.1.2", "demo-worker", true),
		readyPod("cluster-2", "10.244.2.2", "demo-worker2", true),
		readyPod("cluster-3", "10.244.3.2", "demo-worker3", true),
	}
	client := NewClient(fake.NewSimpleClientset(objects...))
	got, err := client.ExpectedPodInventory(context.Background(), "database", []string{"cluster-1", "cluster-2", "cluster-3"}, "demo-control-plane")
	if err != nil {
		t.Fatalf("ExpectedPodInventory() error = %v", err)
	}
	if len(got) != 3 || got[1].IP != "10.244.2.2" || got[2].NodeName != "demo-worker3" {
		t.Fatalf("inventory = %#v", got)
	}
}

func TestExpectedPodInventoryRejectsUnsafeTopology(t *testing.T) {
	tests := []struct {
		name    string
		objects []runtime.Object
	}{
		{name: "not ready", objects: []runtime.Object{readyPod("cluster-1", "10.244.1.2", "demo-worker", false), readyPod("cluster-2", "10.244.2.2", "demo-worker2", true), readyPod("cluster-3", "10.244.3.2", "demo-worker3", true)}},
		{name: "service-like IP missing", objects: []runtime.Object{readyPod("cluster-1", "", "demo-worker", true), readyPod("cluster-2", "10.244.2.2", "demo-worker2", true), readyPod("cluster-3", "10.244.3.2", "demo-worker3", true)}},
		{name: "shared node", objects: []runtime.Object{readyPod("cluster-1", "10.244.1.2", "demo-worker", true), readyPod("cluster-2", "10.244.2.2", "demo-worker", true), readyPod("cluster-3", "10.244.3.2", "demo-worker3", true)}},
		{name: "oracle node", objects: []runtime.Object{readyPod("cluster-1", "10.244.1.2", "demo-control-plane", true), readyPod("cluster-2", "10.244.2.2", "demo-worker2", true), readyPod("cluster-3", "10.244.3.2", "demo-worker3", true)}},
		{name: "extra live database Pod", objects: []runtime.Object{readyPod("cluster-1", "10.244.1.2", "demo-worker", true), readyPod("cluster-2", "10.244.2.2", "demo-worker2", true), readyPod("cluster-3", "10.244.3.2", "demo-worker3", true), readyPod("cluster-4", "10.244.4.2", "demo-worker4", true)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(fake.NewSimpleClientset(tt.objects...))
			if _, err := client.ExpectedPodInventory(context.Background(), "database", []string{"cluster-1", "cluster-2", "cluster-3"}, "demo-control-plane"); err == nil {
				t.Fatal("ExpectedPodInventory() error = nil, want rejection")
			}
		})
	}
}

func TestEndpointSnapshotKeepsAuthorityEvidence(t *testing.T) {
	endpoint := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-rw", Namespace: "database", ResourceVersion: "42",
			Annotations:   map[string]string{"leader": `{"leader":"cluster-1"}`, "history": "[]"},
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "metadata-creator", Operation: metav1.ManagedFieldsOperationUpdate, FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{}}`)}},
				{Manager: "patroni", Operation: metav1.ManagedFieldsOperationUpdate, FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:subsets":{}}`)}},
				{Manager: "foreign-writer", Operation: metav1.ManagedFieldsOperationUpdate, FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:subsets":{}}`)}},
			},
		},
		Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.244.1.2"}}}},
	}
	client := NewClient(fake.NewSimpleClientset(endpoint))
	got, err := client.EndpointSnapshot(context.Background(), "database", "cluster-rw")
	if err != nil {
		t.Fatalf("EndpointSnapshot() error = %v", err)
	}
	if got.ResourceVersion != "42" || got.Addresses[0] != "10.244.1.2" || got.Annotations["history"] != "[]" || len(got.Managers) != 2 || got.Managers[0] != "patroni" || got.Managers[1] != "foreign-writer" || len(got.Raw) == 0 {
		t.Fatalf("snapshot = %#v", got)
	}
}
