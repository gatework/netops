package lease_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gatework/netops/lease"
)

// memStore is an in-memory lease.Store for the example.
type memStore struct {
	mu sync.Mutex
	m  map[string]lease.Lease
}

func (s *memStore) Get(_ context.Context, d string) (lease.Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.m[d]
	return l, ok, nil
}

func (s *memStore) Update(_ context.Context, d string, fn func(lease.Lease, bool) (lease.Lease, bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[d]
	next, keep, err := fn(cur, ok)
	if err != nil {
		return err
	}
	if keep {
		s.m[d] = next
	} else {
		delete(s.m, d)
	}
	return nil
}

func ExampleManager_Acquire() {
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	m := lease.NewManager(&memStore{m: map[string]lease.Lease{}}, func() time.Time { return at })
	ctx := context.Background()
	_, _ = m.Acquire(ctx, []string{"leaf1"}, lease.Holder{Tool: "fabric", Actor: "alice", Purpose: "chg-7"}, 30*time.Minute)
	_, err := m.Acquire(ctx, []string{"leaf1"}, lease.Holder{Tool: "firemon", Actor: "bob"}, time.Minute)
	fmt.Println(errors.Is(err, lease.ErrHeld))
	fmt.Println(err)
	// Output:
	// true
	// lease: leaf1 is held by fabric/alice (chg-7) until 2026-11-22T09:30:00Z
}
