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

package inject

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// ClusterView is the fault-targeting inventory. It is not detector evidence.
type ClusterView struct {
	Leader             Target
	DatabasePods       []Target
	OraclePod          string
	OracleIP           string
	OracleNode         string
	ControlPlaneIP     string
	PeerNodeIPs        []string
	PeerPodCIDRs       []string
	ServiceAPIIP       string
	ExplicitToleration *int64
}

// DiscoverCluster locates one three-Pod Patroni cluster and its oracle. It
// requires the Endpoints leader annotation to name one observed database Pod.
func DiscoverCluster(ctx context.Context, commands *Recorder, contextName, namespace string) (ClusterView, error) {
	result := commands.Run(ctx, []string{"kubectl", "--context", contextName, "-n", namespace, "get", "pods", "-o", "json"}, nil)
	if result.ExitCode != 0 {
		return ClusterView{}, fmt.Errorf("list Pods exit %d: %s", result.ExitCode, result.Stderr)
	}
	var pods podList
	if err := json.Unmarshal([]byte(result.Stdout), &pods); err != nil {
		return ClusterView{}, fmt.Errorf("decode Pods: %w", err)
	}
	view := ClusterView{}
	podByName := make(map[string]Target)
	for _, pod := range pods.Items {
		if pod.Metadata.Labels["cnpatroni.io/component"] == "oracle" {
			if view.OraclePod != "" {
				return ClusterView{}, fmt.Errorf("more than one oracle Pod is present")
			}
			view.OraclePod, view.OracleIP, view.OracleNode = pod.Metadata.Name, pod.Status.PodIP, pod.Spec.NodeName
			continue
		}
		if _, ok := pod.Metadata.Labels["cnpatroni.io/role"]; !ok {
			continue
		}
		target, err := targetFromPod(pod)
		if err != nil {
			return ClusterView{}, err
		}
		view.DatabasePods = append(view.DatabasePods, target)
		podByName[target.Pod] = target
	}
	if view.OraclePod == "" || net.ParseIP(view.OracleIP) == nil {
		return ClusterView{}, fmt.Errorf("one oracle Pod with a literal Pod IP is required")
	}
	if len(view.DatabasePods) != 3 {
		return ClusterView{}, fmt.Errorf("exactly three cnpatroni.io/role database Pods are required, got %d", len(view.DatabasePods))
	}

	endpointsResult := commands.Run(ctx, []string{"kubectl", "--context", contextName, "-n", namespace, "get", "endpoints", "-o", "json"}, nil)
	if endpointsResult.ExitCode != 0 {
		return ClusterView{}, fmt.Errorf("list Endpoints exit %d: %s", endpointsResult.ExitCode, endpointsResult.Stderr)
	}
	var endpoints struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(endpointsResult.Stdout), &endpoints); err != nil {
		return ClusterView{}, fmt.Errorf("decode Endpoints: %w", err)
	}
	var leaderName string
	for _, endpoint := range endpoints.Items {
		if !strings.HasSuffix(endpoint.Metadata.Name, "-rw") {
			continue
		}
		candidate, ok := parseLeaderAnnotation(endpoint.Metadata.Annotations["leader"])
		if !ok {
			continue
		}
		if leaderName != "" && leaderName != candidate {
			return ClusterView{}, fmt.Errorf("multiple Patroni leader annotations disagree")
		}
		leaderName = candidate
	}
	leader, ok := podByName[leaderName]
	if !ok {
		return ClusterView{}, fmt.Errorf("Endpoints leader %q is not one of the three database Pods", leaderName)
	}
	view.Leader = leader
	for _, pod := range pods.Items {
		if pod.Metadata.Name == leader.Pod && pod.Metadata.Annotations["cnpatroni.io/explicitUnreachableToleration"] == "true" {
			for _, toleration := range pod.Spec.Tolerations {
				if toleration.Key == "node.kubernetes.io/unreachable" && toleration.Effect == "NoExecute" {
					view.ExplicitToleration = toleration.TolerationSeconds
				}
			}
		}
	}

	nodesResult := commands.Run(ctx, []string{"kubectl", "--context", contextName, "get", "nodes", "-o", "json"}, nil)
	if nodesResult.ExitCode != 0 {
		return ClusterView{}, fmt.Errorf("list node topology exit %d: %s", nodesResult.ExitCode, nodesResult.Stderr)
	}
	var nodes nodeList
	if err := json.Unmarshal([]byte(nodesResult.Stdout), &nodes); err != nil {
		return ClusterView{}, fmt.Errorf("decode nodes: %w", err)
	}
	databaseNodes := make(map[string]struct{})
	for _, pod := range view.DatabasePods {
		databaseNodes[pod.Node] = struct{}{}
	}
	for _, node := range nodes.Items {
		internalIP := nodeInternalIP(node)
		if _, controlPlane := node.Metadata.Labels["node-role.kubernetes.io/control-plane"]; controlPlane {
			view.ControlPlaneIP = internalIP
		}
		if _, databaseNode := databaseNodes[node.Metadata.Name]; databaseNode && node.Metadata.Name != leader.Node {
			view.PeerNodeIPs = append(view.PeerNodeIPs, internalIP)
			view.PeerPodCIDRs = append(view.PeerPodCIDRs, node.Spec.PodCIDRs...)
		}
	}
	if net.ParseIP(view.ControlPlaneIP) == nil || len(view.PeerNodeIPs) != 2 {
		return ClusterView{}, fmt.Errorf("control-plane IP or two peer database node IPs are missing")
	}
	serviceResult := commands.Run(ctx, []string{"kubectl", "--context", contextName, "-n", "default", "get", "service", "kubernetes", "-o", "json"}, nil)
	if serviceResult.ExitCode != 0 {
		return ClusterView{}, fmt.Errorf("get Kubernetes Service exit %d: %s", serviceResult.ExitCode, serviceResult.Stderr)
	}
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(serviceResult.Stdout), &service); err != nil || net.ParseIP(service.Spec.ClusterIP) == nil {
		return ClusterView{}, fmt.Errorf("decode Kubernetes Service cluster IP")
	}
	view.ServiceAPIIP = service.Spec.ClusterIP
	return view, nil
}

type podList struct {
	Items []podJSON `json:"items"`
}

type podJSON struct {
	Metadata struct {
		Name        string            `json:"name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		NodeName    string `json:"nodeName"`
		Tolerations []struct {
			Key               string `json:"key"`
			Effect            string `json:"effect"`
			TolerationSeconds *int64 `json:"tolerationSeconds"`
		} `json:"tolerations"`
	} `json:"spec"`
	Status struct {
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			ContainerID  string `json:"containerID"`
			RestartCount int32  `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type nodeList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			PodCIDRs []string `json:"podCIDRs"`
		} `json:"spec"`
		Status struct {
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
		} `json:"status"`
	} `json:"items"`
}

func targetFromPod(pod podJSON) (Target, error) {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "patroni" {
			containerID := strings.TrimPrefix(status.ContainerID, "containerd://")
			if containerID == "" || pod.Spec.NodeName == "" || net.ParseIP(pod.Status.PodIP) == nil {
				return Target{}, fmt.Errorf("Pod %q has incomplete direct fault target evidence", pod.Metadata.Name)
			}
			return Target{Pod: pod.Metadata.Name, Container: status.Name, ContainerID: containerID, PodIP: pod.Status.PodIP, Node: pod.Spec.NodeName}, nil
		}
	}
	return Target{}, fmt.Errorf("Pod %q has no patroni container status", pod.Metadata.Name)
}

func parseLeaderAnnotation(raw string) (string, bool) {
	var value map[string]any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return "", false
	}
	for _, key := range []string{"leader", "name"} {
		if leader, ok := value[key].(string); ok && leader != "" {
			return leader, true
		}
	}
	return "", false
}

func nodeInternalIP(node struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		PodCIDRs []string `json:"podCIDRs"`
	} `json:"spec"`
	Status struct {
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
	} `json:"status"`
}) string {
	for _, address := range node.Status.Addresses {
		if address.Type == "InternalIP" {
			return address.Address
		}
	}
	return ""
}
