package keys

import (
	"math/rand/v2"
	"testing"
)

func BenchmarkKeyspacePick(b *testing.B) {
	ks, err := NewKeyspace("bench/4k/", 1000)
	if err != nil {
		b.Fatalf("NewKeyspace: %v", err)
	}
	rng := rand.New(rand.NewPCG(1, 1))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ks.Pick(rng)
	}
}
