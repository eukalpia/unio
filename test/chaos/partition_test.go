package chaos

import (
	"context"
	"testing"
)

// TestPartition disconnects one random node from the internode network while
// the workload runs, reconnects it, and verifies the ledger. Writes must keep
// being acknowledged during the partition and after the heal, and every
// acknowledged write must read back intact.
//
// Requires UNIO_CHAOS=1 and the 4-node cluster running.
func TestPartition(t *testing.T) {
	requireChaos(t)
	env := setupScenario(t)
	stop := startWorkload(t, env)

	if err := awaitAcked(env, 0); err != nil {
		t.Fatalf("no progress before the partition: %v", err)
	}

	victim := env.nodes[env.rng.IntN(len(env.nodes))]
	part := NewPartition(env.network, victim.service)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
		defer cancel()
		if err := part.Heal(ctx); err != nil {
			t.Errorf("heal partition of %s: %v", victim.service, err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()

	before := env.ledger.Count(OutcomeAcked)
	if err := part.Cut(ctx); err != nil {
		t.Fatalf("cut %s from %s: %v", victim.service, env.network.Name(), err)
	}
	if err := awaitAcked(env, before); err != nil {
		t.Fatalf("no progress while %s was partitioned: %v", victim.service, err)
	}
	t.Logf("partitioned %s: writes kept flowing (acked before cut: %d)", victim.service, before)

	if err := part.Heal(ctx); err != nil {
		t.Fatalf("heal %s: %v", victim.service, err)
	}
	if err := WaitReady(ctx, victim.endpoint, readyPollInterval); err != nil {
		t.Fatalf("%s not ready after heal: %v", victim.service, err)
	}
	if err := awaitAcked(env, env.ledger.Count(OutcomeAcked)); err != nil {
		t.Fatalf("no progress after healing %s: %v", victim.service, err)
	}

	stop()
	checkReport(t, env, verifyLedger(t, env))
}
