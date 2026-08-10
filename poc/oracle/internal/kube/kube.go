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

// Package kube contains read-only Kubernetes discovery and evidence capture.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
)

// Client deliberately exposes only read operations used by the oracle.
type Client struct{ client kubernetes.Interface }

// NewClient wraps a Kubernetes interface, primarily for tests.
func NewClient(client kubernetes.Interface) *Client { return &Client{client: client} }

// Build constructs an in-cluster client when running as a Pod and an explicit-
// context kubeconfig client otherwise. VerifyContext must run before other use.
func Build(contextName string) (*Client, error) {
	var (
		config *rest.Config
		err    error
	)
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		config, err = rest.InClusterConfig()
	} else {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return NewClient(clientset), nil
}

// VerifyContext lists every node and applies both independent kind checks.
func (c *Client) VerifyContext(ctx context.Context, contextName string) error {
	nodes, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes for context guard: %w", err)
	}
	views := make([]guard.Node, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		views = append(views, guard.Node{Name: node.Name, ProviderID: node.Spec.ProviderID})
	}
	return guard.ValidateContext(contextName, views)
}

// PodInventory is the minimal direct-connection and topology view.
type PodInventory struct {
	Name              string                   `json:"name"`
	IP                string                   `json:"pod_ip"`
	NodeName          string                   `json:"node_name"`
	Phase             corev1.PodPhase          `json:"phase"`
	Ready             bool                     `json:"ready"`
	Labels            map[string]string        `json:"labels"`
	ContainerStatuses []corev1.ContainerStatus `json:"container_statuses"`
}

// ExpectedPodInventory requires exactly three Ready Pods on distinct non-
// oracle nodes and literal, non-loopback Pod IPs.
func (c *Client) ExpectedPodInventory(ctx context.Context, namespace string, expected []string, oracleNode string) ([]PodInventory, error) {
	if len(expected) != 3 {
		return nil, fmt.Errorf("preflight requires exactly three expected database Pods, got %d", len(expected))
	}
	live, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "cnpatroni.io/role"})
	if err != nil {
		return nil, fmt.Errorf("list live database Pods: %w", err)
	}
	liveNames := make([]string, 0, len(live.Items))
	for _, pod := range live.Items {
		liveNames = append(liveNames, pod.Name)
	}
	expectedNames := append([]string(nil), expected...)
	sort.Strings(liveNames)
	sort.Strings(expectedNames)
	if !slices.Equal(liveNames, expectedNames) {
		return nil, fmt.Errorf("live database Pods=%v differ from expected Pods=%v", liveNames, expectedNames)
	}
	result := make([]PodInventory, 0, len(expected))
	nodes := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get expected Pod %q: %w", name, err)
		}
		ready := podReady(pod)
		if pod.Status.Phase != corev1.PodRunning || !ready {
			return nil, fmt.Errorf("expected Pod %q is phase=%s ready=%t", name, pod.Status.Phase, ready)
		}
		ip := net.ParseIP(pod.Status.PodIP)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return nil, fmt.Errorf("expected Pod %q has non-routable literal Pod IP %q", name, pod.Status.PodIP)
		}
		if pod.Spec.NodeName == "" || pod.Spec.NodeName == oracleNode {
			return nil, fmt.Errorf("expected Pod %q is on forbidden node %q", name, pod.Spec.NodeName)
		}
		if _, duplicate := nodes[pod.Spec.NodeName]; duplicate {
			return nil, fmt.Errorf("multiple expected Pods use node %q", pod.Spec.NodeName)
		}
		nodes[pod.Spec.NodeName] = struct{}{}
		result = append(result, PodInventory{
			Name: pod.Name, IP: ip.String(), NodeName: pod.Spec.NodeName, Phase: pod.Status.Phase,
			Ready: ready, Labels: copyStringMap(pod.Labels), ContainerStatuses: append([]corev1.ContainerStatus(nil), pod.Status.ContainerStatuses...),
		})
	}
	return result, nil
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// EndpointEvidence retains both DCS annotations and Service routing state.
type EndpointEvidence struct {
	ResourceVersion string            `json:"resource_version"`
	Annotations     map[string]string `json:"annotations"`
	Addresses       []string          `json:"addresses"`
	Managers        []string          `json:"managed_fields_managers"`
	Raw             json.RawMessage   `json:"raw"`
}

// EndpointSnapshot reads the Patroni-owned leader lock and routing object once.
func (c *Client) EndpointSnapshot(ctx context.Context, namespace, scope string) (EndpointEvidence, error) {
	endpoint, err := c.client.CoreV1().Endpoints(namespace).Get(ctx, scope, metav1.GetOptions{})
	if err != nil {
		return EndpointEvidence{}, fmt.Errorf("get Patroni Endpoints %q: %w", scope, err)
	}
	raw, err := json.Marshal(endpoint)
	if err != nil {
		return EndpointEvidence{}, fmt.Errorf("marshal Patroni Endpoints: %w", err)
	}
	result := EndpointEvidence{ResourceVersion: endpoint.ResourceVersion, Annotations: copyStringMap(endpoint.Annotations), Raw: raw}
	for _, subset := range endpoint.Subsets {
		for _, address := range subset.Addresses {
			result.Addresses = append(result.Addresses, address.IP)
		}
	}
	for _, field := range endpoint.ManagedFields {
		if field.FieldsV1 != nil && bytes.Contains(field.FieldsV1.Raw, []byte(`"f:subsets"`)) {
			result.Managers = append(result.Managers, field.Manager)
		}
	}
	sort.Strings(result.Addresses)
	return result, nil
}

// Core returns the underlying interface for additional read-only samplers.
func (c *Client) Core() kubernetes.Interface { return c.client }

func copyStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
