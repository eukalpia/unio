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
	c.put("a", 1)
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
	c.put("a", 1)
	c.put("b", 2)
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
		c.put(i, i)
		if n := len(c.cur) + len(c.prev); n > 2*size {
			t.Fatalf("cache holds %d entries, bound is %d", n, 2*size)
		}
	}
}

func TestTTLCacheKeepsHotEntries(t *testing.T) {
	clock := newClock()
	c := newTTLCache[string, int](clock.Now, time.Hour, 4)
	c.put("hot", 7)
	for i := range 200 {
		c.put(fmt.Sprint("cold", i), i)
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
