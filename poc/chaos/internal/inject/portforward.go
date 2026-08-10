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
	"fmt"
	"net"
	"strconv"
	"time"
)

// PortForward is a recorded kubectl port-forward to the oracle control port.
type PortForward struct {
	URL    string
	cancel context.CancelFunc
	done   <-chan Execution
}

// StartPortForward reserves a loopback port, starts the exact kubectl command,
// and waits until the listener is reachable.
func StartPortForward(ctx context.Context, commands *Recorder, contextName, namespace, pod string, remotePort int) (*PortForward, error) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	localPort := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		return nil, err
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	done := make(chan Execution, 1)
	argv := []string{"kubectl", "--context", contextName, "-n", namespace, "port-forward", "--address", "127.0.0.1", "pod/" + pod, strconv.Itoa(localPort) + ":" + strconv.Itoa(remotePort)}
	go func() { done <- commands.Run(forwardCtx, argv, nil) }()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case result := <-done:
			cancel()
			return nil, fmt.Errorf("oracle port-forward exited early with %d: %s", result.ExitCode, result.Stderr)
		case <-deadline.C:
			cancel()
			return nil, fmt.Errorf("oracle port-forward did not listen within 10s")
		case <-ticker.C:
			conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return &PortForward{URL: "http://" + address, cancel: cancel, done: done}, nil
			}
		}
	}
}

// Close stops the forward and waits for its command record to flush.
func (p *PortForward) Close() {
	if p == nil {
		return
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}
