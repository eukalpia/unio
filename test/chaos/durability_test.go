package chaos

import (
	"context"
	"testing"
	"time"
)

// TestDurability runs a concurrent PUT workload while random nodes are killed
// with SIGKILL and restarted, one at a time. It then kills every node at once,
// restarts them and verifies the whole ledger: acknowledged writes must be
// durable and intact (I1, I2) and rejected writes must stay invisible (I4).
//
// Requires UNIO_CHAOS=1 and the 4-node cluster running.
func TestDurability(t *testing.T) {
	requireChaos(t)
	env := setupScenario(t)
	t.Logf("random phase duration %s", env.cfg.Duration)
	stop := startWorkload(t, env)

	if err := awaitAcked(env, 0); err != nil {
		t.Fatalf("no progress before the first crash: %v", err)
	}
	runRandomPhase(t, env)
	finalPhase(t, env, stop)
}

// runRandomPhase kills and restarts random nodes at random intervals until the
// configured duration has passed. Only one node is down at any time.
func runRandomPhase(t *testing.T, env *scenarioEnv) {
	t.Helper()
	phase := time.NewTimer(env.cfg.Duration)
	defer phase.Stop()
	for round := 1; ; round++ {
		wait := killIntervalMin + time.Duration(env.rng.Int64N(int64(killIntervalMax-killIntervalMin)))
		interval := time.NewTimer(wait)
		select {
		case <-phase.C:
			interval.Stop()
			return
		case <-interval.C:
		}
		victim := env.nodes[env.rng.IntN(len(env.nodes))]
		crashAndRecover(t, env, round, victim)
	}
}

// crashAndRecover SIGKILLs one node, checks that the writes keep flowing
// without it, then restarts it and waits until it is ready.
func crashAndRecover(t *testing.T, env *scenarioEnv, round int, victim scenarioNode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()

	before := env.ledger.Count(OutcomeAcked)
	if err := env.cluster.Kill(ctx, victim.service); err != nil {
		t.Fatalf("round %d: kill %s: %v", round, victim.service, err)
	}
	if err := env.cluster.AwaitState(ctx, victim.service, "exited"); err != nil {
		t.Fatalf("round %d: %s is still running after SIGKILL (check for a restart policy in the compose file): %v", round, victim.service, err)
	}
	if err := awaitAcked(env, before); err != nil {
		t.Fatalf("round %d: no progress while %s was down: %v", round, victim.service, err)
	}
	t.Logf("round %d: SIGKILL %s, writes kept flowing (acked before crash: %d)", round, victim.service, before)

	if err := env.cluster.Start(ctx, victim.service); err != nil {
		t.Fatalf("round %d: start %s: %v", round, victim.service, err)
	}
	readyCtx, cancelReady := context.WithTimeout(context.Background(), readyTimeout)
	defer cancelReady()
	if err := WaitReady(readyCtx, victim.endpoint, readyPollInterval); err != nil {
		t.Fatalf("round %d: %s not ready after restart: %v", round, victim.service, err)
	}
}

// finalPhase kills the whole cluster at once while writes are in flight,
// restarts it, and verifies the ledger.
func finalPhase(t *testing.T, env *scenarioEnv, stop func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()

	if err := env.cluster.KillAll(ctx); err != nil {
		t.Fatalf("kill all nodes: %v", err)
	}
	stop() // writes still in flight fail and are recorded as unknown
	if err := env.cluster.StartAll(ctx); err != nil {
		t.Fatalf("start all nodes: %v", err)
	}
	if err := waitAllReady(env, readyTimeout); err != nil {
		t.Fatalf("cluster not ready after simultaneous crash: %v", err)
	}
	checkReport(t, env, verifyLedger(t, env))
}
