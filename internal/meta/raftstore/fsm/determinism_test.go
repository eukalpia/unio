package fsm

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// sequenceLength is the number of commands in the determinism sequence.
const sequenceLength = 900

// selectorRange returns the command selectors that target kind: the data
// group takes selectors 0..8 and the system group 9..14.
func selectorRange(kind Kind) (first, count int) {
	if kind == KindSystem {
		return 9, numCommandKinds - 9
	}
	return 0, 9
}

// generatedSequence returns a fixed pseudo-random sequence of commands for
// kind. One command in three is fully random, which covers invalid input, and
// the others are built from valid names and keys so that the state fills up.
// The same seed always gives the same sequence.
func generatedSequence(kind Kind, seed int64, n int) []*pb.Command {
	rng := rand.New(rand.NewSource(seed))
	first, count := selectorRange(kind)
	names := []string{"bkt", "seedbkt", "bucket-two", "Bad", "", "ab", strings.Repeat("n", 70)}
	keyNames := []string{"seed", "a/b/c", "", "\xff", "k\x00x", "key-with-\x01-byte"}
	versions := []string{"", "v1", "seed-up", strings.Repeat("v", 65)}
	validNames := []string{"bkt", "seedbkt", "bucket-two"}
	out := make([]*pb.Command, 0, n)
	for i := 0; i < n; i++ {
		payload := make([]byte, rng.Intn(40))
		rng.Read(payload)
		p := genParams{
			bucket:  uint64(rng.Intn(3)),
			name:    names[rng.Intn(len(names))],
			key:     keyNames[rng.Intn(len(keyNames))],
			version: versions[rng.Intn(len(versions))],
			number:  uint32(rng.Intn(10003)),
			payload: payload,
			flags:   uint8(rng.Intn(256)),
		}
		sel := uint8(first + rng.Intn(count))
		if i%3 != 2 {
			p.name = validNames[rng.Intn(len(validNames))]
			p.key = fmt.Sprintf("obj/%03d", rng.Intn(150))
			p.version = ""
			p.flags = 0
			p.bucket = uint64(1 + rng.Intn(2))
		} else if rng.Intn(4) == 0 {
			sel = uint8(rng.Intn(numCommandKinds))
		}
		out = append(out, buildCommand(sel, p))
	}
	return out
}

// dumpAll returns every key and value of the database, including the applied
// index, so that two replicas can be compared byte for byte.
func dumpAll(t *testing.T, f *FSM) map[string]string {
	t.Helper()
	it, err := f.db.NewIter(nil)
	if err != nil {
		t.Fatalf("iterate: %v", err)
	}
	out := make(map[string]string)
	for ok := it.First(); ok; ok = it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	if err := closeIter(it, it.Error()); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return out
}

// objectKeys counts the object version keys of a database dump.
func objectKeys(db map[string]string) int {
	n := 0
	for k := range db {
		if strings.HasPrefix(k, string(keys.PrefixObject)) {
			n++
		}
	}
	return n
}

// replay applies the sequence to a fresh state machine of kind and returns
// the outcomes and the full database contents.
func replay(t *testing.T, kind Kind, cmds []*pb.Command) ([]*Outcome, map[string]string) {
	t.Helper()
	h := newHarness(t, kind)
	fuzzPrelude(t, h)
	outs := make([]*Outcome, 0, len(cmds))
	for _, cmd := range cmds {
		outs = append(outs, h.do(cmd))
	}
	return outs, dumpAll(t, h.f)
}

// TestSameCommandsYieldIdenticalState applies one sequence of valid and
// invalid commands to two fresh state machines of each kind. The outcomes
// must match, and the databases must be byte-identical, including the
// applied index and the encoding of every stored record.
func TestSameCommandsYieldIdenticalState(t *testing.T) {
	for _, kind := range []Kind{KindData, KindSystem} {
		t.Run(fmt.Sprintf("kind%d", kind), func(t *testing.T) {
			cmds := generatedSequence(kind, 7, sequenceLength)
			outsA, dbA := replay(t, kind, cmds)
			outsB, dbB := replay(t, kind, cmds)
			accepted := 0
			for i := range outsA {
				a, b := outsA[i], outsB[i]
				if fmt.Sprint(a.Err) != fmt.Sprint(b.Err) {
					t.Fatalf("command %d: error %v on one replica, %v on the other", i, a.Err, b.Err)
				}
				if !proto.Equal(a.Result, b.Result) {
					t.Fatalf("command %d: results differ", i)
				}
				if a.Err == nil {
					accepted++
				}
			}
			if accepted == 0 || accepted == len(outsA) {
				t.Fatalf("sequence accepted %d of %d commands; it must mix valid and invalid ones", accepted, len(outsA))
			}
			if kind == KindData && objectKeys(dbA) < 20 {
				t.Fatalf("sequence stored %d object versions, want at least 20 so that the comparison covers records", objectKeys(dbA))
			}
			if len(dbA) != len(dbB) {
				t.Fatalf("database sizes differ: %d and %d keys", len(dbA), len(dbB))
			}
			for k, v := range dbA {
				if dbB[k] != v {
					t.Fatalf("database differs at key %x", k)
				}
			}
		})
	}
}
