package iam

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"go.uber.org/goleak"
)

func TestTTLCacheExpiresEntries(t *testing.T) {
	clock := newClock()
	c := newTTLCache[string, int](clock.Now, 10*time.Second, 8)
	putNow(c, "a", 1)
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Fatal("fresh entry missing")
	}
	clock.Advance(9 * time.Second)
	if _, ok := c.get("a"); !ok {
		t.Fatal("entry expired before its TTL")
	}
	clock.Advance(time.Second)
	if _, ok := c.get("a"); ok {
		t.Fatal("entry served at its expiry instant")
	}
}

func TestTTLCacheRemoveAndClear(t *testing.T) {
	clock := newClock()
	c := newTTLCache[string, int](clock.Now, time.Minute, 8)
	putNow(c, "a", 1)
	putNow(c, "b", 2)
	c.remove("a")
	if _, ok := c.get("a"); ok {
		t.Fatal("removed entry still cached")
	}
	c.clear()
	if _, ok := c.get("b"); ok {
		t.Fatal("entry survived clear")
	}
}

func TestTTLCacheIsBounded(t *testing.T) {
	clock := newClock()
	const size = 16
	c := newTTLCache[int, int](clock.Now, time.Hour, size)
	for i := range 5000 {
		putNow(c, i, i)
		if n := len(c.cur) + len(c.prev); n > 2*size {
			t.Fatalf("cache holds %d entries, bound is %d", n, 2*size)
		}
	}
}

func TestTTLCacheKeepsHotEntries(t *testing.T) {
	clock := newClock()
	c := newTTLCache[string, int](clock.Now, time.Hour, 4)
	putNow(c, "hot", 7)
	for i := range 200 {
		putNow(c, fmt.Sprint("cold", i), i)
		if _, ok := c.get("hot"); !ok {
			t.Fatalf("hot entry evicted after %d inserts", i+1)
		}
	}
}

// TestConcurrentUse drives the store from several goroutines at once: key
// creation, authentication, authorization and deletion. It must not race or
// leak goroutines, and every operation must return a defined result.
func TestConcurrentUse(t *testing.T) {
	defer goleak.VerifyNone(t)
	s, _, clock := newTestStore(t)
	ctx := context.Background()
	const workers = 8
	const rounds = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user := fmt.Sprintf("user%d", w)
			if _, err := s.CreateUser(ctx, user); err != nil {
				errs <- err
				return
			}
			if err := s.SetUserPolicy(ctx, user, []byte(readAllowPolicy)); err != nil {
				errs <- err
				return
			}
			for range rounds {
				key, err := s.CreateAccessKey(ctx, user)
				if err != nil {
					errs <- err
					return
				}
				req, err := signRequestE(key.ID, key.Secret, clock.Now())
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.Authenticate(ctx, req); err != nil {
					errs <- fmt.Errorf("authenticate: %w", err)
					return
				}
				if err := s.Authorize(ctx, identityOf(key, user), "s3:GetObject", "photos", "a.jpg"); err != nil {
					errs <- fmt.Errorf("authorize: %w", err)
					return
				}
				if err := s.DeleteAccessKey(ctx, key.ID); err != nil {
					errs <- err
					return
				}
			}
			if err := s.DeleteUser(ctx, user); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// identityOf returns the identity an authenticated request of key carries.
func identityOf(key auth.AccessKey, user string) auth.Identity {
	return auth.Identity{AccessKey: key.ID, User: user}
}

// putNow caches v for k as a fill that started now. It is the test form of
// putAt for fills that cannot race with an invalidation.
func putNow[K comparable, V any](c *ttlCache[K, V], k K, v V) bool {
	return c.putAt(k, v, c.epoch())
}

// TestTTLCacheRejectsFillStartedBeforeInvalidation forces the interleaving: a
// fill reads its epoch, an invalidation runs, and then the fill tries to
// insert the value it loaded before the invalidation.
func TestTTLCacheRejectsFillStartedBeforeInvalidation(t *testing.T) {
	clock := newClock()
	c := newTTLCache[string, int](clock.Now, time.Minute, 8)
	for name, invalidate := range map[string]func(){
		"remove": func() { c.remove("a") },
		"clear":  func() { c.clear() },
	} {
		t.Run(name, func(t *testing.T) {
			fill := c.epoch()
			invalidate()
			if c.putAt("a", 1, fill) {
				t.Fatal("a value loaded before the invalidation was cached")
			}
			if _, ok := c.get("a"); ok {
				t.Fatal("stale value visible after the invalidation")
			}
			if !putNow(c, "a", 2) {
				t.Fatal("a fill that started after the invalidation was rejected")
			}
		})
	}
}
