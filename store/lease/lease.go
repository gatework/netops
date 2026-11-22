package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gatework/netops/lease"
	"github.com/gatework/netops/store"
)

const lockWait = 10 * time.Second

// Store keeps leases under a root.
type Store struct {
	st *store.Store
}

var _ lease.Store = (*Store)(nil)

// Open opens or creates the lease root.
func Open(root string) (*Store, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return &Store{st: st}, nil
}

func rel(device string) string { return "leases/" + store.Name(device) + ".json" }

// Get returns the stored lease of device.
func (s *Store) Get(ctx context.Context, device string) (lease.Lease, bool, error) {
	var l lease.Lease
	err := s.st.ReadJSON(ctx, rel(device), 64<<10, &l)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return lease.Lease{}, false, nil
	case err != nil:
		return lease.Lease{}, false, fmt.Errorf("lease store: %w", err)
	}
	return l, true, nil
}

// Update applies fn under the device lock.
func (s *Store) Update(ctx context.Context, device string, fn func(lease.Lease, bool) (lease.Lease, bool, error)) error {
	lock, err := s.st.Lock(ctx, "lease/"+device, lockWait)
	if err != nil {
		return fmt.Errorf("lease store: %w", err)
	}
	defer lock.Close()
	cur, present, err := s.Get(ctx, device)
	if err != nil {
		return err
	}
	next, keep, err := fn(cur, present)
	if err != nil {
		return err
	}
	switch {
	case keep:
		if _, err := s.st.WriteJSON(ctx, rel(device), next); err != nil {
			return fmt.Errorf("lease store: %w", err)
		}
	case present:
		if err := s.st.Remove(rel(device)); err != nil {
			return fmt.Errorf("lease store: %w", err)
		}
	}
	return nil
}
