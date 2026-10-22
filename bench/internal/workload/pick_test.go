package workload

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestPickRatioMatchesWeights(t *testing.T) {
	const draws = 200_000
	tests := []struct {
		name    string
		weights []int
	}{
		{name: "70/30", weights: []int{70, 30}},
		{name: "equal", weights: []int{1, 1}},
		{name: "three way", weights: []int{5, 3, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := make([]OpSpec, len(tc.weights))
			total := 0
			for i, w := range tc.weights {
				ops[i] = OpSpec{Name: "op", Weight: w}
				total += w
			}
			rng := rand.New(rand.NewPCG(7, 0))
			counts := make([]int, len(ops))
			for i := 0; i < draws; i++ {
				counts[pick(rng, ops)]++
			}
			for i, w := range tc.weights {
				want := float64(w) / float64(total)
				got := float64(counts[i]) / draws
				if math.Abs(got-want) > 0.01 {
					t.Errorf("op %d share = %.4f, want %.4f within 0.01", i, got, want)
				}
			}
		})
	}
}

func TestPickSingleOperationIsAlwaysZero(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	ops := []OpSpec{{Name: "PUT", Weight: 1}}
	for i := 0; i < 100; i++ {
		if got := pick(rng, ops); got != 0 {
			t.Fatalf("pick = %d, want 0", got)
		}
	}
}

func TestPickIsReproducibleForSeed(t *testing.T) {
	ops := []OpSpec{{Name: "GET", Weight: 70}, {Name: "PUT", Weight: 30}}
	a := rand.New(rand.NewPCG(42, 3))
	b := rand.New(rand.NewPCG(42, 3))
	for i := 0; i < 1000; i++ {
		if pick(a, ops) != pick(b, ops) {
			t.Fatalf("sequences diverge at draw %d", i)
		}
	}
}
