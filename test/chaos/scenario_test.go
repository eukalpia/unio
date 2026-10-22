package chaos

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"
)

// Scenario tuning. Durations are wall-clock bounds for the cluster to react,
// not synchronisation sleeps: every wait is a channel or context wait.
const (
	scenarioWorkers   = 8
	requestTimeout    = 10 * time.Second
	readyPollInterval = 500 * time.Millisecond
	setupReadyTimeout = 30 * time.Second
	readyTimeout      = 3 * time.Minute
	controlTimeout    = 2 * time.Minute
	verifyTimeout     = 10 * time.Minute
	progressStep      = 25 // acknowledged writes that count as progress
	progressTimeout   = 90 * time.Second
	killIntervalMin   = 2 * time.Second
	killIntervalMax   = 8 * time.Second
	violationLimit    = 20
	// seedMix decorrelates the schedule stream from the payload seed.
	seedMix uint64 = 0x9e3779b97f4a7c15
)

// scenarioNode is one node of the cluster: its Compose service, its S3
// endpoint and a client bound to that endpoint.
type scenarioNode struct {
	service  string
	endpoint string
	client   *S3Client
}

// scenarioEnv is the state shared by the cluster scenarios.
type scenarioEnv struct {
	cfg      Config
	cluster  *Cluster
	network  *Network
	nodes    []scenarioNode
	ledger   *Ledger
	workload *Workload
	rng      *mrand.Rand
}

// requireChaos skips the test unless the cluster scenarios are enabled.
func requireChaos(t *testing.T) {
	t.Helper()
	if os.Getenv("UNIO_CHAOS") != "1" {
		t.Skip("cluster chaos scenario skipped: set UNIO_CHAOS=1 with the 4-node cluster running (see test/chaos/README.md)")
	}
}

// setupScenario loads the configuration, checks that every node answers,
// creates the bucket and registers the restore step. The seed and run id are
// logged so that a failing schedule can be replayed.
func setupScenario(t *testing.T) *scenarioEnv {
	t.Helper()
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	if !cfg.SeedSet {
		cfg.Seed = mrand.Uint64()
	}
	runID, err := newRunID()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("run=%s seed=%d (rerun with UNIO_CHAOS_SEED=%d to replay the schedule and payloads)", runID, cfg.Seed, cfg.Seed)

	cluster, err := NewCluster(NewExecRunner(), cfg.ComposeFile, cfg.Project, len(cfg.Endpoints))
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	env := &scenarioEnv{
		cfg:     cfg,
		cluster: cluster,
		network: NewNetwork(cluster),
		ledger:  NewLedger(),
		rng:     mrand.New(mrand.NewPCG(cfg.Seed, cfg.Seed^seedMix)),
	}
	// Registered first so that it runs last, after the workload has stopped
	// and any partition has been healed.
	t.Cleanup(func() { restoreCluster(t, env) })

	writers := make([]ObjectWriter, 0, len(cfg.Endpoints))
	for i, ep := range cfg.Endpoints {
		client, err := NewS3Client(ep, cfg.Bucket, cfg.AccessKey, cfg.SecretKey, requestTimeout)
		if err != nil {
			t.Fatalf("client for %s: %v", ep, err)
		}
		t.Cleanup(func() { _ = client.Close() })
		env.nodes = append(env.nodes, scenarioNode{service: cluster.Nodes()[i], endpoint: ep, client: client})
		writers = append(writers, client)
	}

	ctx, cancel := context.WithTimeout(context.Background(), setupReadyTimeout)
	defer cancel()
	for _, n := range env.nodes {
		if err := WaitReady(ctx, n.endpoint, readyPollInterval); err != nil {
			t.Fatalf("cluster not reachable; start it with: docker compose -f deploy/compose/cluster-4x4.yml --env-file deploy/compose/.env up -d --build (%v)", err)
		}
	}
	if err := env.nodes[0].client.EnsureBucket(ctx); err != nil {
		t.Fatalf("bucket: %v", err)
	}

	env.workload, err = NewWorkload(WorkloadConfig{RunID: runID, Seed: cfg.Seed, Workers: scenarioWorkers}, writers, env.ledger)
	if err != nil {
		t.Fatalf("workload: %v", err)
	}
	return env
}

// startWorkload runs the PUT loops in the background. The returned stop
// function cancels them and waits for them to exit. It is idempotent and is
// also registered in t.Cleanup.
func startWorkload(t *testing.T, env *scenarioEnv) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.workload.Run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

// awaitAcked blocks until the workload has acknowledged progressStep writes
// beyond from, or until progressTimeout elapses. It proves the cluster still
// accepts writes at the moment the caller checks.
func awaitAcked(env *scenarioEnv, from int) error {
	ctx, cancel := context.WithTimeout(context.Background(), progressTimeout)
	defer cancel()
	return env.ledger.WaitFor(ctx, OutcomeAcked, from+progressStep)
}

// waitAllReady polls the readiness endpoint of every node until each one
// answers 200 or the timeout elapses.
func waitAllReady(env *scenarioEnv, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, n := range env.nodes {
		if err := WaitReady(ctx, n.endpoint, readyPollInterval); err != nil {
			return fmt.Errorf("%s: %w", n.service, err)
		}
	}
	return nil
}

// verifyLedger reads back the whole ledger, spreading the reads over every
// node. The workload must already be stopped.
func verifyLedger(t *testing.T, env *scenarioEnv) Report {
	t.Helper()
	readers := make([]ObjectClient, 0, len(env.nodes))
	for _, n := range env.nodes {
		readers = append(readers, n.client)
	}
	rr, err := RoundRobin(readers...)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	report, err := Verify(ctx, rr, env.ledger)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return report
}

// checkReport fails the test when the report shows a violation, and when the
// run acknowledged no writes at all, which would make the check vacuous.
func checkReport(t *testing.T, env *scenarioEnv, report Report) {
	t.Helper()
	t.Logf("ledger: acked=%d rejected=%d unknown=%d verified=%d violations=%d (seed %d)",
		report.Acked, report.Rejected, report.Unknown, report.Verified, len(report.Violations), env.cfg.Seed)
	if report.Acked == 0 {
		t.Fatal("no acknowledged writes: the run verified nothing (check credentials, bucket and cluster health)")
	}
	for i, v := range report.Violations {
		if i == violationLimit {
			t.Errorf("... and %d more violations", len(report.Violations)-violationLimit)
			break
		}
		t.Errorf("%s violated for %s: %s", v.Invariant, v.Key, v.Reason)
	}
	if len(report.Violations) > 0 {
		t.Fatalf("%d invariant violation(s) (seed %d)", len(report.Violations), env.cfg.Seed)
	}
}

// restoreCluster starts every node and waits until all of them answer. It
// runs in t.Cleanup, so it uses its own context.
func restoreCluster(t *testing.T, env *scenarioEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := env.cluster.StartAll(ctx); err != nil {
		t.Errorf("restore: start nodes: %v", err)
		return
	}
	if err := waitAllReady(env, readyTimeout); err != nil {
		t.Errorf("restore: %v", err)
	}
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", fmt.Errorf("run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
