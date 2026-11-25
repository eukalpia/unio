package remotedrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// flood is far more zero-progress reads than any retry bound tolerates. tolerated
// is a count a sane source may produce before its data.
const (
	flood     = 10000
	tolerated = 20
)

// step is one scripted answer of a scripted reader. A step with no data and no
// error is a zero-progress read: it returns (0, nil).
type step struct {
	data string
	err  error
}

// scripted answers Read calls from a script. After the script ends it returns
// io.EOF.
type scripted struct {
	steps []step
}

// Read implements io.Reader.
func (s *scripted) Read(p []byte) (int, error) {
	if len(s.steps) == 0 {
		return 0, io.EOF
	}
	st := &s.steps[0]
	if len(st.data) > 0 {
		n := copy(p, st.data)
		st.data = st.data[n:]
		if len(st.data) == 0 {
			s.steps = s.steps[1:]
		}
		return n, nil
	}
	s.steps = s.steps[1:]
	return 0, st.err
}

// zeroReads returns n zero-progress steps.
func zeroReads(n int) []step {
	return make([]step, n)
}

// concat joins scripted steps.
func concat(parts ...[]step) []step {
	var out []step
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// TestExactReaderEndOfSource checks that an exact source is accepted only when
// its end is established, and that every inconclusive end is refused. A probe
// that makes no progress is inconclusive: it must never commit the declared
// prefix.
func TestExactReaderEndOfSource(t *testing.T) {
	boom := errors.New("source failed")
	cases := []struct {
		name    string
		steps   []step
		size    int64
		wantErr error // nil means the read must succeed with the full data
	}{
		{
			name:  "exact source ends with EOF",
			steps: []step{{data: "abcdef"}},
			size:  6,
		},
		{
			name:  "zero-progress reads before the end are tolerated",
			steps: concat([]step{{data: "abcdef"}}, zeroReads(tolerated), nil),
			size:  6,
		},
		{
			name:    "longer source found after zero-progress reads",
			steps:   concat([]step{{data: "abcdef"}}, zeroReads(tolerated), []step{{data: "x"}}),
			size:    6,
			wantErr: errSourceLonger,
		},
		{
			name:    "probe that never makes progress is refused",
			steps:   concat([]step{{data: "abcdef"}}, zeroReads(flood)),
			size:    6,
			wantErr: io.ErrNoProgress,
		},
		{
			name:    "probe that fails is refused",
			steps:   []step{{data: "abcdef"}, {err: boom}},
			size:    6,
			wantErr: boom,
		},
		{
			name:    "source that never makes progress before its data is refused",
			steps:   concat(zeroReads(flood), []step{{data: "abcdef"}}),
			size:    6,
			wantErr: io.ErrNoProgress,
		},
		{
			name:  "zero-progress reads before the data are tolerated",
			steps: concat(zeroReads(tolerated), []step{{data: "abcdef"}}),
			size:  6,
		},
		{
			name:    "source ending early is refused",
			steps:   []step{{data: "abc"}},
			size:    6,
			wantErr: io.ErrUnexpectedEOF,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &exactReader{r: &scripted{steps: tc.steps}, remaining: tc.size}
			got, err := io.ReadAll(r)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("read %q with error %v, want %v", got, err, tc.wantErr)
				}
				if bytes.Equal(got, []byte("abcdef")) {
					t.Fatal("inconclusive end still delivered the whole declared prefix")
				}
				return
			}
			if err != nil || string(got) != "abcdef" {
				t.Fatalf("read %q, %v; want the full prefix", got, err)
			}
		})
	}
}

// TestWriteShardNeverCommitsOnInconclusiveProbe checks the contract end to end:
// a source with a longer tail, whose probe makes no progress before the tail
// appears, must fail the write, and nothing may be stored.
func TestWriteShardNeverCommitsOnInconclusiveProbe(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	_, client := f.serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id := ids.ShardID{Data: ids.NewDataID(), Index: 1}
	src := &scripted{steps: concat([]step{{data: "abcdef"}}, zeroReads(flood), []step{{data: "x"}})}
	if err := client.WriteShard(ctx, id, src, 6); err == nil {
		t.Fatal("write of an overlong source reported success")
	}
	if _, err := client.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("overlong source left a committed shard behind: %v", err)
	}
}
