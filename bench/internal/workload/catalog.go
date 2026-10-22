package workload

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eukalpia/unio/bench/internal/objstore"
)

// Object sizes, in bytes, for the object workloads.
const (
	size4K  = 4 << 10
	size1M  = 1 << 20
	size64M = 64 << 20
)

// Keyspace sizes. Small and medium objects use 1000 keys. 64 MiB objects use
// 32 keys so that the pre-created data stays near 2 GiB per cluster.
const (
	keysSmall = 1000
	keysLarge = 32
)

// Env holds what the workloads need to build their specs.
type Env struct {
	// Store is the endpoint under test.
	Store objstore.Store
	// Bucket is the bucket that holds every object.
	Bucket string
	// Concurrency bounds the parallel pre-fill requests. It must be at least 1.
	Concurrency int
	// Seed makes payloads and key choices reproducible.
	Seed uint64
	// ListKeys is the number of objects pre-filled for the list workload.
	ListKeys int
	// ListPage is the MaxKeys of each LIST request, between 1 and 1000.
	ListPage int
}

// Names returns every workload name in canonical order.
func Names() []string {
	return []string{"put-4k", "get-4k", "put-1m", "get-1m", "put-64m", "get-64m", "list", "mixed"}
}

// ParseNames expands a comma-separated workload list. "all" selects every
// workload in canonical order and cannot be combined with other names.
// Duplicates are dropped and the remaining names keep the order given.
func ParseNames(list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		return nil, errors.New("no workload given")
	}
	fields := strings.Split(list, ",")
	var out []string
	for _, f := range fields {
		name := strings.TrimSpace(f)
		if name == "" {
			return nil, fmt.Errorf("empty workload name in %q", list)
		}
		if name == "all" {
			if len(fields) > 1 {
				return nil, errors.New(`"all" cannot be combined with other workloads`)
			}
			return Names(), nil
		}
		if !slices.Contains(Names(), name) {
			return nil, fmt.Errorf("unknown workload %q; valid names are %s, or all", name, strings.Join(Names(), ", "))
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// Build returns the Spec for the named workload.
func Build(name string, env Env) (Spec, error) {
	if env.Store == nil {
		return Spec{}, errors.New("workload env has no store")
	}
	if env.Bucket == "" {
		return Spec{}, errors.New("workload env has no bucket")
	}
	if env.Concurrency < 1 {
		return Spec{}, fmt.Errorf("workload env concurrency must be at least 1, got %d", env.Concurrency)
	}
	put := weightedKind{name: "PUT", weight: 1, kind: kindPut}
	get := weightedKind{name: "GET", weight: 1, kind: kindGet}
	switch name {
	case "put-4k":
		return objectSpec(name, size4K, keysSmall, "4k", env, []weightedKind{put}, false)
	case "get-4k":
		return objectSpec(name, size4K, keysSmall, "4k", env, []weightedKind{get}, true)
	case "put-1m":
		return objectSpec(name, size1M, keysSmall, "1m", env, []weightedKind{put}, false)
	case "get-1m":
		return objectSpec(name, size1M, keysSmall, "1m", env, []weightedKind{get}, true)
	case "put-64m":
		return objectSpec(name, size64M, keysLarge, "64m", env, []weightedKind{put}, false)
	case "get-64m":
		return objectSpec(name, size64M, keysLarge, "64m", env, []weightedKind{get}, true)
	case "mixed":
		// 70% GET and 30% PUT of 4 KiB objects, on the keyspace GET reads.
		mixGet := weightedKind{name: "GET", weight: 70, kind: kindGet}
		mixPut := weightedKind{name: "PUT", weight: 30, kind: kindPut}
		return objectSpec(name, size4K, keysSmall, "4k", env, []weightedKind{mixGet, mixPut}, true)
	case "list":
		return listSpec(env)
	default:
		return Spec{}, fmt.Errorf("unknown workload %q", name)
	}
}
