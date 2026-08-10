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

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/bundle"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/control"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/kube"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/postgres"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/replay"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/resolve"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/round"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/sample"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/verdict"
)

const resolverGrace = 10 * time.Second

// RunResult includes the detector output and any harness error.
type RunResult struct {
	Verdict verdict.RunVerdict
	Err     error
}

type runManifest struct {
	RunID         string              `json:"run_id,omitempty"`
	Scenario      string              `json:"scenario,omitempty"`
	Start         time.Time           `json:"start"`
	End           *time.Time          `json:"end,omitempty"`
	KubeContext   string              `json:"kube_context"`
	Namespace     string              `json:"namespace"`
	Scope         string              `json:"scope"`
	ExpectedNodes []string            `json:"expected_nodes"`
	PodInventory  []kube.PodInventory `json:"pod_inventory"`
	Authority     *AuthorityPreflight `json:"authority_preflight,omitempty"`
	RoundPeriod   string              `json:"round_period"`
	Duration      string              `json:"duration"`
	FinalVerdict  verdict.Status      `json:"final_verdict,omitempty"`
	Deviations    []string            `json:"deviations"`
	Caveats       []string            `json:"caveats"`
}

type nodeRuntime struct {
	info       kube.PodInventory
	config     *pgx.ConnConfig
	writeConn  *pgx.Conn
	sampleConn *pgx.Conn
	gate       round.NodeGate
	mu         sync.Mutex
	lineage    lineageToken
}

type lineageToken struct {
	SystemIdentifier uint64
	PostmasterStart  time.Time
	ContainerID      string
	RestartCount     int32
	OK               bool
}

type runEvidence struct {
	mu         sync.Mutex
	attempts   []model.Attempt
	invariants []model.InvariantInstant
	authority  []model.AuthoritySample
	bundleErr  error
}

func (e *runEvidence) setBundleError(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.bundleErr == nil {
		e.bundleErr = err
	}
}

func (e *runEvidence) addAttempt(writer *bundle.Writer, attempt model.Attempt) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts = append(e.attempts, attempt)
	if err := writer.AppendJSONL("rounds.jsonl", attempt); err != nil && e.bundleErr == nil {
		e.bundleErr = err
	}
}

func (e *runEvidence) addInvariant(invariant model.InvariantInstant) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.invariants = append(e.invariants, invariant)
}

func (e *runEvidence) addAuthority(authority model.AuthoritySample) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.authority = append(e.authority, authority)
}

type markSink struct {
	writer      *bundle.Writer
	evidence    *runEvidence
	partitioned atomic.Bool
	mu          sync.Mutex
	marks       []model.FaultMark
}

func (s *markSink) AppendMark(mark control.Mark) error {
	lowerLabel := strings.ToLower(mark.Label)
	if strings.HasPrefix(lowerLabel, "partition-") {
		s.partitioned.Store(true)
	}
	if strings.HasPrefix(lowerLabel, "heal-partition-") && mark.Phase == "after" {
		s.partitioned.Store(false)
	}
	s.mu.Lock()
	s.marks = append(s.marks, model.FaultMark{At: model.Instant(mark.MonoNS), Label: mark.Label, Phase: mark.Phase})
	s.mu.Unlock()
	return s.writer.AppendJSONL("marks.jsonl", mark)
}

func (s *markSink) snapshot() []model.FaultMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.FaultMark(nil), s.marks...)
}

func (s *markSink) AppendContainerObservation(observation control.ContainerObservation) error {
	if err := s.writer.AppendJSONL("samples/container-"+observation.Node+".jsonl", observation); err != nil {
		return err
	}
	state := model.NodeInvariant{}
	if observation.OK {
		state.PID1OK, state.PID1Comm = true, observation.PID1Comm
	}
	s.evidence.addInvariant(model.InvariantInstant{
		At: model.Instant(observation.MonoNS), FaultWindow: s.partitioned.Load(),
		Nodes: map[string]model.NodeInvariant{observation.Node: state},
	})
	return nil
}

// RunLive runs preflight rounds, direct-Pod measurement rounds, continuous
// sampling, in-doubt resolution, reconciliation, and the pure detector.
func RunLive(ctx context.Context, config Config, password string) RunResult {
	harnessVerdict := verdict.RunVerdict{Status: verdict.HarnessError, ExitCode: 3}
	if err := config.Validate(); err != nil {
		return RunResult{Verdict: harnessVerdict, Err: err}
	}
	writer, err := bundle.NewWriter(config.BundleDir, []string{password})
	if err != nil {
		return RunResult{Verdict: harnessVerdict, Err: err}
	}
	origin := time.Now()
	manifest := runManifest{
		RunID: filepathBase(config.BundleDir), Scenario: os.Getenv("CNPATRONI_SCENARIO"), Start: origin.UTC(),
		KubeContext: config.Context, Namespace: config.Namespace, Scope: config.Scope,
		ExpectedNodes: append([]string(nil), config.ExpectedNodes...), RoundPeriod: config.RoundPeriod.String(), Duration: config.Duration.String(),
		Deviations: []string{
			"D1 — two effective direct-Pod commits are fatal without an authority-claim conjunct.",
			"D2 — a durably resolved but unacknowledged commit counts as a writer.",
		},
		Caveats: []string{
			"pg_control_checkpoint().timeline_id can lag the current timeline immediately after promotion and is forensic data only.",
			"clock_timestamp() is the database node wall clock and is never used for ordering or overlap decisions.",
		},
	}
	if err := writer.WriteJSON("manifest.json", manifest); err != nil {
		return RunResult{Verdict: harnessVerdict, Err: err}
	}
	if err := verifyDetectorControls(); err != nil {
		return failBundle(writer, manifest, err)
	}

	kubeClient, err := kube.Build(config.Context)
	if err != nil {
		return failBundle(writer, manifest, err)
	}
	if err := kubeClient.VerifyContext(ctx, config.Context); err != nil {
		return failBundle(writer, manifest, err)
	}
	oracleNode := os.Getenv("NODE_NAME")
	if oracleNode == "" {
		return failBundle(writer, manifest, fmt.Errorf("NODE_NAME is required to prove database Pods are off the oracle node"))
	}
	inventory, err := kubeClient.ExpectedPodInventory(ctx, config.Namespace, config.ExpectedNodes, oracleNode)
	if err != nil {
		return failBundle(writer, manifest, err)
	}
	manifest.PodInventory = inventory
	authority, err := validateAuthorityLive(ctx, config, inventory, kubeClient)
	if err != nil {
		return failBundle(writer, manifest, err)
	}
	manifest.Authority = &authority
	if err := writer.WriteJSON("manifest.json", manifest); err != nil {
		return RunResult{Verdict: harnessVerdict, Err: err}
	}

	runtimes := make([]*nodeRuntime, 0, len(inventory))
	for _, pod := range inventory {
		pgConfig, configErr := postgres.BuildConfig(pod.IP, uint16(config.PGPort), "postgres", "cnpatroni_chaos", password, config.EffectiveAttemptTimeout())
		if configErr != nil {
			return failBundle(writer, manifest, configErr)
		}
		runtime := &nodeRuntime{info: pod, config: pgConfig}
		if err := runtime.ensureConnections(ctx); err != nil {
			return failBundle(writer, manifest, fmt.Errorf("connect expected Pod %q: %w", pod.Name, err))
		}
		runtime.refreshLineage(ctx)
		runtimes = append(runtimes, runtime)
	}
	defer closeRuntimes(context.Background(), runtimes)

	evidence := &runEvidence{}
	markWriter := &markSink{writer: writer, evidence: evidence}
	preflight := make([]RoundEvidence, 0, config.PreflightRounds)
	for index := 0; index < config.PreflightRounds; index++ {
		target := origin.Add(time.Duration(index) * config.RoundPeriod)
		if err := waitUntil(ctx, target); err != nil {
			return failBundle(writer, manifest, err)
		}
		roundEvidence := executeRound(ctx, runtimes, int64(index+1), origin, config.EffectiveAttemptTimeout())
		preflight = append(preflight, roundEvidence)
		for _, attempt := range roundEvidence.Attempts {
			evidence.addAttempt(writer, attempt)
		}
		evidence.setBundleError(writer.AppendJSONL("round-summaries.jsonl", map[string]any{"round_id": index + 1, "dispatch_skew_ns": roundEvidence.DispatchSkew.Nanoseconds(), "preflight": true}))
	}
	if err := ValidateSteadyState(config.ExpectedNodes, preflight, config.PreflightRounds); err != nil {
		return failBundle(writer, manifest, err)
	}
	server, listener, err := startControlServer(config.ControlPort, origin, markWriter)
	if err != nil {
		return failBundle(writer, manifest, err)
	}
	defer server.Shutdown(context.Background())
	defer listener.Close()

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	schema := sample.NewSchemaTracker()
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		runSampler(runCtx, origin, config, kubeClient, runtimes, writer, evidence, schema, markWriter)
	}()
	var reconnectWG sync.WaitGroup
	reconnectWG.Add(1)
	go func() {
		defer reconnectWG.Done()
		runReconnectors(runCtx, runtimes)
	}()

	runStart := time.Now()
	rounds := int(config.Duration / config.RoundPeriod)
	if rounds < 1 {
		rounds = 1
	}
	var resolverWG sync.WaitGroup
	var roundWG sync.WaitGroup
	roundResults := make(chan RoundEvidence, rounds)
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		for roundEvidence := range roundResults {
			roundID := int64(0)
			if len(roundEvidence.Attempts) > 0 {
				roundID = roundEvidence.Attempts[0].RoundID
			}
			evidence.setBundleError(writer.AppendJSONL("round-summaries.jsonl", map[string]any{"round_id": roundID, "dispatch_skew_ns": roundEvidence.DispatchSkew.Nanoseconds(), "preflight": false}))
			for attemptIndex := range roundEvidence.Attempts {
				attempt := roundEvidence.Attempts[attemptIndex]
				runtime := runtimeByName(runtimes, attempt.NodeID)
				if attempt.Outcome != model.InDoubt {
					evidence.addAttempt(writer, attempt)
					continue
				}
				baseline := roundEvidence.Lineages[attempt.NodeID]
				resolverWG.Add(1)
				go func() {
					defer resolverWG.Done()
					resolverCtx, cancel := context.WithDeadline(context.Background(), runStart.Add(config.Duration+resolverGrace))
					defer cancel()
					adapter := resolve.Resolver{Lookup: &databaseLookup{runtime: runtime, kube: kubeClient, namespace: config.Namespace, baseline: baseline}}
					evidence.addAttempt(writer, adapter.Resolve(resolverCtx, attempt))
				}()
			}
		}
	}()
	var schedulingErr error
	for index := 0; index < rounds; index++ {
		target := runStart.Add(time.Duration(index) * config.RoundPeriod)
		if err := waitUntil(ctx, target); err != nil {
			schedulingErr = err
			break
		}
		roundID := int64(config.PreflightRounds + index + 1)
		fireRoundAsync(ctx, runtimes, roundID, origin, config.EffectiveAttemptTimeout(), roundResults, &roundWG)
	}
	roundWG.Wait()
	close(roundResults)
	<-collectorDone
	cancelRun()
	samplerWG.Wait()
	reconnectWG.Wait()
	resolverWG.Wait()
	if schedulingErr != nil {
		return failBundle(writer, manifest, schedulingErr)
	}
	if err := writer.WriteJSON("schema/patroni-rest-observed.json", schema.Snapshot()); err != nil {
		evidence.bundleErr = err
	}

	lost, reconcile, durable := reconcileSurvey(ctx, runtimes, evidence.attempts)
	evidence.mu.Lock()
	var lateResolved []map[string]any
	for index := range evidence.attempts {
		attempt := &evidence.attempts[index]
		if attempt.Outcome != model.InDoubt || attempt.Resolution == model.ResolvedCommitted {
			continue
		}
		if _, present := durable[fmt.Sprintf("%d\x00%s", attempt.RoundID, attempt.NodeID)]; present {
			attempt.Resolution = model.ResolvedCommitted
			attempt.ResolverNote = "late post-run survey found durable row"
			lateResolved = append(lateResolved, map[string]any{"round_id": attempt.RoundID, "node_id": attempt.NodeID, "resolution": model.ResolvedCommitted})
		}
	}
	evidence.mu.Unlock()
	reconcile["late_resolutions"] = lateResolved
	if err := writer.WriteJSON("reconcile.json", reconcile); err != nil {
		evidence.bundleErr = err
	}
	evidence.mu.Lock()
	podIPs := make(map[string]string, len(runtimes))
	for _, runtime := range runtimes {
		podIPs[runtime.info.Name] = runtime.info.IP
	}
	detectorInput := verdict.Input{
		Scenario:      os.Getenv("CNPATRONI_SCENARIO"),
		ExpectedNodes: config.ExpectedNodes, Attempts: append([]model.Attempt(nil), evidence.attempts...),
		Invariants: append([]model.InvariantInstant(nil), evidence.invariants...), Authority: append([]model.AuthoritySample(nil), evidence.authority...),
		Marks: markWriter.snapshot(), PodIPs: podIPs,
		BundleWriteFailed: evidence.bundleErr != nil, LostAcknowledged: lost, LostWriteFatal: config.LostWriteSeverity == "fatal",
	}
	evidence.mu.Unlock()
	finalVerdict, detectErr := verdict.Detect(detectorInput)
	if detectErr != nil && finalVerdict.ExitCode != 3 {
		return failBundle(writer, manifest, detectErr)
	}
	if err := writer.WriteJSON("verdict.json", finalVerdict); err != nil {
		return failBundle(writer, manifest, err)
	}
	if err := writer.Write("verdict.md", []byte(renderVerdict(finalVerdict))); err != nil {
		return failBundle(writer, manifest, err)
	}
	ended := time.Now().UTC()
	manifest.End, manifest.FinalVerdict = &ended, finalVerdict.Status
	if err := writer.WriteJSON("manifest.json", manifest); err != nil {
		return RunResult{Verdict: harnessVerdict, Err: err}
	}
	return RunResult{Verdict: finalVerdict, Err: detectErr}
}

func (r *nodeRuntime) ensureConnections(ctx context.Context) error {
	writeConn, err := pgx.ConnectConfig(ctx, r.config.Copy())
	if err != nil {
		return err
	}
	sampleConn, err := pgx.ConnectConfig(ctx, r.config.Copy())
	if err != nil {
		_ = writeConn.Close(context.Background())
		return err
	}
	r.mu.Lock()
	r.writeConn, r.sampleConn = writeConn, sampleConn
	r.mu.Unlock()
	return nil
}

func (r *nodeRuntime) ensureWriteConnection(ctx context.Context) error {
	r.mu.Lock()
	if r.writeConn != nil && !r.writeConn.IsClosed() {
		r.mu.Unlock()
		return nil
	}
	r.writeConn = nil
	r.mu.Unlock()
	connectCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	conn, err := pgx.ConnectConfig(connectCtx, r.config.Copy())
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.writeConn = conn
	r.mu.Unlock()
	return nil
}

func (r *nodeRuntime) ensureSampleConnection(ctx context.Context) error {
	r.mu.Lock()
	if r.sampleConn != nil && !r.sampleConn.IsClosed() {
		r.mu.Unlock()
		return nil
	}
	r.sampleConn = nil
	r.mu.Unlock()
	conn, err := pgx.ConnectConfig(ctx, r.config.Copy())
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.sampleConn = conn
	r.mu.Unlock()
	r.refreshLineage(ctx)
	return nil
}

func runReconnectors(ctx context.Context, runtimes []*nodeRuntime) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, runtime := range runtimes {
			connectCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
			_ = runtime.ensureWriteConnection(connectCtx)
			cancel()
			sampleCtx, sampleCancel := context.WithTimeout(ctx, 80*time.Millisecond)
			_ = runtime.ensureSampleConnection(sampleCtx)
			sampleCancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *nodeRuntime) refreshLineage(ctx context.Context) {
	r.mu.Lock()
	conn := r.sampleConn
	r.mu.Unlock()
	if conn == nil || conn.IsClosed() {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var systemID uint64
	var postmaster time.Time
	if conn.QueryRow(queryCtx, guard.SelectSystemIdentifier, pgx.QueryExecModeExec).Scan(&systemID) != nil ||
		conn.QueryRow(queryCtx, guard.SelectPostmasterStart, pgx.QueryExecModeExec).Scan(&postmaster) != nil {
		return
	}
	containerID, restartCount := containerLineage(r.info.ContainerStatuses)
	r.mu.Lock()
	r.lineage = lineageToken{SystemIdentifier: systemID, PostmasterStart: postmaster, ContainerID: containerID, RestartCount: restartCount, OK: true}
	r.mu.Unlock()
}

func (r *nodeRuntime) lineageSnapshot() lineageToken {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lineage
}

// attemptWrite runs one node's write attempt for a round and publishes the
// result. The synchronous and asynchronous round drivers differ only in the
// WaitGroup they report completion to, so they share this body: a divergence
// between them would be a divergence in what the write oracle measures.
//
// The deferred calls stay in this order deliberately. Defers run last in,
// first out, so the in-flight gate is released before the caller's WaitGroup
// reports the attempt finished.
func attemptWrite(
	ctx context.Context,
	runtime *nodeRuntime,
	roundID int64,
	origin time.Time,
	timeout time.Duration,
	release <-chan struct{},
	results chan<- model.Attempt,
	done *sync.WaitGroup,
) {
	defer done.Done()
	defer runtime.gate.Done()
	<-release
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runtime.mu.Lock()
	conn := runtime.writeConn
	runtime.mu.Unlock()
	attempt := postgres.Writer{NodeID: runtime.info.Name, Exec: conn, Origin: origin, Now: time.Now}.Attempt(attemptCtx, roundID)
	if attempt.Outcome == model.InDoubt && conn != nil {
		runtime.mu.Lock()
		if runtime.writeConn == conn {
			runtime.writeConn = nil
		}
		runtime.mu.Unlock()
		_ = conn.Close(context.Background())
	}
	results <- attempt
}

// noAttempt records that a node's previous write attempt was still in flight
// when this round fired, so the round holds no measurement for that node. Both
// round drivers report it identically: a round that silently omitted the node
// would read as a node that was never asked to write.
func noAttempt(runtime *nodeRuntime, roundID int64, origin time.Time) model.Attempt {
	instant := model.Instant(time.Since(origin).Nanoseconds())

	return model.Attempt{RoundID: roundID, NodeID: runtime.info.Name, DispatchAt: instant, SettleAt: instant, Outcome: model.NoAttempt, Error: "previous attempt is still in flight"}
}

func executeRound(ctx context.Context, runtimes []*nodeRuntime, roundID int64, origin time.Time, timeout time.Duration) RoundEvidence {
	release := make(chan struct{})
	results := make(chan model.Attempt, len(runtimes))
	var wg sync.WaitGroup
	for _, runtime := range runtimes {
		if !runtime.gate.TryStart() {
			results <- noAttempt(runtime, roundID, origin)

			continue
		}
		wg.Add(1)
		go attemptWrite(ctx, runtime, roundID, origin, timeout, release, results, &wg)
	}
	close(release)
	wg.Wait()
	close(results)
	evidence := RoundEvidence{}
	var dispatches []model.Instant
	for attempt := range results {
		evidence.Attempts = append(evidence.Attempts, attempt)
		dispatches = append(dispatches, attempt.DispatchAt)
	}
	sort.Slice(evidence.Attempts, func(i, j int) bool { return evidence.Attempts[i].NodeID < evidence.Attempts[j].NodeID })
	evidence.DispatchSkew = round.DispatchSkew(dispatches)
	return evidence
}

func fireRoundAsync(ctx context.Context, runtimes []*nodeRuntime, roundID int64, origin time.Time, timeout time.Duration, output chan<- RoundEvidence, coordinators *sync.WaitGroup) {
	release := make(chan struct{})
	results := make(chan model.Attempt, len(runtimes))
	lineages := make(map[string]lineageToken, len(runtimes))
	var attempts sync.WaitGroup
	for _, runtime := range runtimes {
		lineages[runtime.info.Name] = runtime.lineageSnapshot()
		if !runtime.gate.TryStart() {
			results <- noAttempt(runtime, roundID, origin)

			continue
		}
		attempts.Add(1)
		go attemptWrite(ctx, runtime, roundID, origin, timeout, release, results, &attempts)
	}
	close(release)
	coordinators.Add(1)
	go func() {
		defer coordinators.Done()
		attempts.Wait()
		close(results)
		evidence := RoundEvidence{Lineages: lineages}
		var dispatches []model.Instant
		for attempt := range results {
			evidence.Attempts = append(evidence.Attempts, attempt)
			dispatches = append(dispatches, attempt.DispatchAt)
		}
		sort.Slice(evidence.Attempts, func(i, j int) bool { return evidence.Attempts[i].NodeID < evidence.Attempts[j].NodeID })
		evidence.DispatchSkew = round.DispatchSkew(dispatches)
		output <- evidence
	}()
}

func runSampler(ctx context.Context, origin time.Time, config Config, kubeClient *kube.Client, runtimes []*nodeRuntime, writer *bundle.Writer, evidence *runEvidence, schema *sample.SchemaTracker, marks *markSink) {
	var watchWG sync.WaitGroup
	watchWG.Add(4)
	go watchResource(ctx, &watchWG, writer, evidence, "samples/k8s-pods.jsonl", func() (watch.Interface, error) {
		return kubeClient.Core().CoreV1().Pods(config.Namespace).Watch(ctx, metav1.ListOptions{})
	})
	go watchResource(ctx, &watchWG, writer, evidence, "samples/k8s-endpoints.jsonl", func() (watch.Interface, error) {
		return kubeClient.Core().CoreV1().Endpoints(config.Namespace).Watch(ctx, metav1.ListOptions{})
	})
	go watchResource(ctx, &watchWG, writer, evidence, "samples/k8s-events.jsonl", func() (watch.Interface, error) {
		return kubeClient.Core().CoreV1().Events(config.Namespace).Watch(ctx, metav1.ListOptions{})
	})
	go watchResource(ctx, &watchWG, writer, evidence, "samples/k8s-endpointslices.jsonl", func() (watch.Interface, error) {
		return kubeClient.Core().DiscoveryV1().EndpointSlices(config.Namespace).Watch(ctx, metav1.ListOptions{})
	})
	defer watchWG.Wait()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		taken := time.Now()
		invariant := model.InvariantInstant{At: model.Instant(taken.Sub(origin).Nanoseconds()), FaultWindow: marks.partitioned.Load(), PostgresSample: true, Nodes: make(map[string]model.NodeInvariant)}
		var mu sync.Mutex
		failsafeActive := make(map[string]bool)
		failsafeActiveKnown := make(map[string]bool)
		failsafeMembers := make(map[string]int)
		failsafeMembersKnown := make(map[string]bool)
		var wg sync.WaitGroup
		for _, runtime := range runtimes {
			runtime := runtime
			wg.Add(1)
			go func() {
				defer wg.Done()
				runtime.mu.Lock()
				conn := runtime.sampleConn
				runtime.mu.Unlock()
				record := sample.SQLSampler{Node: runtime.info.Name, Conn: conn, Origin: origin, Now: time.Now}.Sample(ctx)
				evidence.setBundleError(writer.AppendJSONL("samples/sql-"+runtime.info.Name+".jsonl", record))
				state := model.NodeInvariant{}
				if record.View.InRecovery != nil {
					state.RecoveryOK, state.InRecovery = true, *record.View.InRecovery
				}
				mu.Lock()
				invariant.Nodes[runtime.info.Name] = state
				mu.Unlock()
			}()
			for _, path := range guard.PatroniPaths() {
				path := path
				wg.Add(1)
				go func() {
					defer wg.Done()
					record := (sample.RESTSampler{Port: config.PatroniPort, Origin: origin, Now: time.Now}).Sample(ctx, runtime.info.Name, runtime.info.IP, path)
					evidence.setBundleError(writer.AppendJSONL("samples/patroni-"+runtime.info.Name+".jsonl", record))
					schema.Observe(path, []byte(record.Raw))
					if path == "/patroni" && record.OK {
						view := sample.ParsePatroni([]byte(record.Raw))
						claim := model.ClaimUnknown
						if view.RoleKnown {
							if view.Role == "primary" || view.Role == "standby_leader" {
								claim = model.ClaimPrimary
							} else {
								claim = model.ClaimNotPrimary
							}
						}
						evidence.addAuthority(model.AuthoritySample{NodeID: runtime.info.Name, At: model.Instant(record.MonoNS), Claim: claim})
						if rawActive, ok := view.Fields["failsafe_mode_is_active"]; ok {
							var active bool
							if json.Unmarshal(rawActive, &active) == nil {
								mu.Lock()
								failsafeActiveKnown[runtime.info.Name], failsafeActive[runtime.info.Name] = true, active
								mu.Unlock()
							}
						}
					}
					if path == "/failsafe" && record.OK {
						var members map[string]json.RawMessage
						if json.Unmarshal([]byte(record.Raw), &members) == nil {
							mu.Lock()
							failsafeMembersKnown[runtime.info.Name], failsafeMembers[runtime.info.Name] = true, len(members)
							mu.Unlock()
						}
					}
				}()
			}
		}
		wg.Wait()
		for _, runtime := range runtimes {
			state := invariant.Nodes[runtime.info.Name]
			if failsafeActiveKnown[runtime.info.Name] && failsafeMembersKnown[runtime.info.Name] {
				state.FailsafeOK = true
				state.FailsafeConfirmed = failsafeActive[runtime.info.Name] && failsafeMembers[runtime.info.Name] >= len(runtimes)-1
			}
			invariant.Nodes[runtime.info.Name] = state
		}
		endpoint, endpointErr := kubeClient.EndpointSnapshot(ctx, config.Namespace, config.Scope)
		if endpointErr == nil {
			evidence.setBundleError(writer.AppendJSONL("samples/k8s-endpoints.jsonl", endpoint))
			invariant.EndpointOK = true
			invariant.EndpointAddresses = append([]string(nil), endpoint.Addresses...)
			for node, state := range invariant.Nodes {
				state.EndpointManagersOK, state.EndpointManagers = true, append([]string(nil), endpoint.Managers...)
				state.DCSOK, state.DCSLeader = dcsLeader(endpoint.Annotations)
				invariant.Nodes[node] = state
			}
		} else {
			evidence.setBundleError(writer.AppendJSONL("samples/k8s-endpoints.jsonl", map[string]any{"taken_at": taken.UTC(), "ok": false, "error": endpointErr.Error()}))
		}
		pods, podsErr := kubeClient.Core().CoreV1().Pods(config.Namespace).List(ctx, metav1.ListOptions{})
		if podsErr == nil {
			evidence.setBundleError(writer.AppendJSONL("samples/k8s-pods.jsonl", map[string]any{"taken_at": taken.UTC(), "mono_ns": taken.Sub(origin).Nanoseconds(), "ok": true, "stale_candidate": marks.partitioned.Load(), "raw": pods}))
		}
		events, eventsErr := kubeClient.Core().CoreV1().Events(config.Namespace).List(ctx, metav1.ListOptions{})
		if eventsErr == nil {
			evidence.setBundleError(writer.AppendJSONL("samples/k8s-events.jsonl", map[string]any{"taken_at": taken.UTC(), "mono_ns": taken.Sub(origin).Nanoseconds(), "ok": true, "raw": events}))
		}
		evidence.addInvariant(invariant)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func watchResource(ctx context.Context, wg *sync.WaitGroup, writer *bundle.Writer, evidence *runEvidence, path string, start func() (watch.Interface, error)) {
	defer wg.Done()
	for ctx.Err() == nil {
		stream, err := start()
		if err != nil {
			evidence.setBundleError(writer.AppendJSONL(path, map[string]any{"taken_at": time.Now().UTC(), "ok": false, "error": err.Error(), "source": "watch"}))
			if waitUntil(ctx, time.Now().Add(time.Second)) != nil {
				return
			}
			continue
		}
		for {
			select {
			case <-ctx.Done():
				stream.Stop()
				return
			case event, ok := <-stream.ResultChan():
				if !ok {
					stream.Stop()
					goto reconnect
				}
				evidence.setBundleError(writer.AppendJSONL(path, map[string]any{"taken_at": time.Now().UTC(), "ok": true, "source": "watch", "event_type": event.Type, "raw": event.Object}))
			}
		}
	reconnect:
	}
}

type databaseLookup struct {
	runtime   *nodeRuntime
	kube      *kube.Client
	namespace string
	baseline  lineageToken
}

func (l *databaseLookup) Lookup(ctx context.Context, attempt model.Attempt) (bool, bool, error) {
	conn, err := pgx.ConnectConfig(ctx, l.runtime.config.Copy())
	if err != nil {
		return false, false, err
	}
	defer conn.Close(context.Background())
	var one int
	err = conn.QueryRow(ctx, guard.ResolverSelect, pgx.QueryExecModeExec, attempt.RoundID, attempt.NodeID).Scan(&one)
	present := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, false, err
	}
	var current lineageToken
	if err := conn.QueryRow(ctx, guard.SelectSystemIdentifier, pgx.QueryExecModeExec).Scan(&current.SystemIdentifier); err != nil {
		return present, false, err
	}
	if err := conn.QueryRow(ctx, guard.SelectPostmasterStart, pgx.QueryExecModeExec).Scan(&current.PostmasterStart); err != nil {
		return present, false, err
	}
	pod, err := l.kube.Core().CoreV1().Pods(l.namespace).Get(ctx, l.runtime.info.Name, metav1.GetOptions{})
	if err != nil {
		return present, false, err
	}
	current.ContainerID, current.RestartCount = containerLineage(pod.Status.ContainerStatuses)
	current.OK = true
	lineageOK := l.baseline.OK && current.OK && l.baseline.SystemIdentifier == current.SystemIdentifier &&
		l.baseline.PostmasterStart.Equal(current.PostmasterStart) && l.baseline.ContainerID == current.ContainerID && l.baseline.RestartCount == current.RestartCount
	return present, lineageOK, nil
}

func reconcileSurvey(ctx context.Context, runtimes []*nodeRuntime, attempts []model.Attempt) ([]verdict.LostWrite, map[string]any, map[string]struct{}) {
	durable := make(map[string]struct{})
	perNode := make(map[string]any)
	for _, runtime := range runtimes {
		conn, err := pgx.ConnectConfig(ctx, runtime.config.Copy())
		if err != nil {
			perNode[runtime.info.Name] = map[string]any{"ok": false, "error": err.Error()}
			continue
		}
		rows, err := conn.Query(ctx, guard.SelectCommitSurvey, pgx.QueryExecModeExec)
		if err != nil {
			perNode[runtime.info.Name] = map[string]any{"ok": false, "error": err.Error()}
			_ = conn.Close(context.Background())
			continue
		}
		var records []map[string]any
		for rows.Next() {
			var roundID, timeline int64
			var nodeID string
			var observedAt time.Time
			if rows.Scan(&roundID, &nodeID, &timeline, &observedAt) != nil {
				continue
			}
			durable[fmt.Sprintf("%d\x00%s", roundID, nodeID)] = struct{}{}
			records = append(records, map[string]any{"round_id": roundID, "node_id": nodeID, "timeline": timeline, "observed_at": observedAt})
		}
		rows.Close()
		_ = conn.Close(context.Background())
		perNode[runtime.info.Name] = map[string]any{"ok": true, "commits": records}
	}
	var lost []verdict.LostWrite
	for _, attempt := range attempts {
		if attempt.Outcome != model.Committed {
			continue
		}
		if _, ok := durable[fmt.Sprintf("%d\x00%s", attempt.RoundID, attempt.NodeID)]; !ok {
			lost = append(lost, verdict.LostWrite{RoundID: attempt.RoundID, NodeID: attempt.NodeID})
		}
	}
	return lost, map[string]any{"nodes": perNode, "acked_write_lost": lost}, durable
}

func dcsLeader(annotations map[string]string) (bool, string) {
	raw, ok := annotations["leader"]
	if !ok {
		return true, ""
	}
	var object map[string]any
	if json.Unmarshal([]byte(raw), &object) != nil {
		return false, ""
	}
	for _, key := range []string{"leader", "name"} {
		if value, ok := object[key].(string); ok {
			return true, value
		}
	}
	return false, ""
}

func containerLineage(statuses []corev1.ContainerStatus) (string, int32) {
	for _, status := range statuses {
		if status.Name == "patroni" {
			return status.ContainerID, status.RestartCount
		}
	}
	if len(statuses) > 0 {
		return statuses[0].ContainerID, statuses[0].RestartCount
	}
	return "", 0
}

func startControlServer(port int, origin time.Time, sink *markSink) (*http.Server, net.Listener, error) {
	podIP := os.Getenv("POD_IP")
	ip := net.ParseIP(podIP)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return nil, nil, fmt.Errorf("POD_IP %q is not a Pod-routable literal IP", podIP)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)))
	if err != nil {
		return nil, nil, fmt.Errorf("bind oracle control port to Pod IP: %w", err)
	}
	server := &http.Server{Handler: control.NewHandler(origin, time.Now, sink), ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return server, listener, nil
}

func waitUntil(ctx context.Context, target time.Time) error {
	delay := time.Until(target)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runtimeByName(runtimes []*nodeRuntime, name string) *nodeRuntime {
	for _, runtime := range runtimes {
		if runtime.info.Name == name {
			return runtime
		}
	}
	return nil
}

func closeRuntimes(ctx context.Context, runtimes []*nodeRuntime) {
	for _, runtime := range runtimes {
		runtime.mu.Lock()
		writeConn, sampleConn := runtime.writeConn, runtime.sampleConn
		runtime.mu.Unlock()
		if writeConn != nil {
			_ = writeConn.Close(ctx)
		}
		if sampleConn != nil {
			_ = sampleConn.Close(ctx)
		}
	}
}

func failBundle(writer *bundle.Writer, manifest runManifest, err error) RunResult {
	ended := time.Now().UTC()
	manifest.End, manifest.FinalVerdict = &ended, verdict.HarnessError
	_ = writer.WriteJSON("manifest.json", manifest)
	harnessVerdict := verdict.RunVerdict{Status: verdict.HarnessError, ExitCode: 3, Taints: []string{"HARNESS_ERROR"}}
	_ = writer.WriteJSON("verdict.json", harnessVerdict)
	return RunResult{Verdict: harnessVerdict, Err: err}
}

func renderVerdict(result verdict.RunVerdict) string {
	return fmt.Sprintf("# Verdict\n\nStatus: %s\n\nExit code: %d\n\nRounds: %d\n\nSafety violations: %d\n\nTaints: %s\n", result.Status, result.ExitCode, len(result.Rounds), len(result.Violations), strings.Join(result.Taints, ", "))
}

func filepathBase(path string) string {
	trimmed := strings.TrimRight(path, string(os.PathSeparator))
	if index := strings.LastIndex(trimmed, string(os.PathSeparator)); index >= 0 {
		return trimmed[index+1:]
	}
	return trimmed
}

func verifyDetectorControls() error {
	root := os.Getenv("CNPATRONI_FIXTURE_ROOT")
	if root == "" {
		for _, candidate := range []string{"testdata/fixtures", "../../testdata/fixtures", "/usr/share/cnpatroni-oracle/fixtures"} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				root = candidate
				break
			}
		}
	}
	if root == "" {
		return fmt.Errorf("detector fixture root is unavailable")
	}
	for name, expectedExit := range map[string]int{"dual-commit-run": 1, "clean-run": 0, "indoubt-run": 2} {
		result, err := replay.LoadAndDetect(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("replay detector control %s: %w", name, err)
		}
		if result.ExitCode != expectedExit {
			return fmt.Errorf("detector control %s exit=%d, want %d", name, result.ExitCode, expectedExit)
		}
	}
	return nil
}
