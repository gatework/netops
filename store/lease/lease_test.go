package lease_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/netops/lease"
	filelease "github.com/gatework/netops/store/lease"
)

func TestManager_fileStore(t *testing.T) {
	t.Parallel()
	st, err := filelease.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	m := lease.NewManager(st, func() time.Time { return time.Unix(0, clock.Load()) })
	fabric := lease.Holder{Tool: "fabric", Actor: "alice", Purpose: "chg-1"}
	wlan := lease.Holder{Tool: "wireless", Actor: "bob", Purpose: "kick"}
	ctx := t.Context()

	set, err := m.Acquire(ctx, []string{"sw2", "sw1", "sw1"}, fabric, 10*time.Minute)
	if err != nil || len(set.Leases) != 2 {
		t.Fatalf("acquire %v %v", set, err)
	}
	_, err = m.Acquire(ctx, []string{"sw0", "sw1"}, wlan, time.Minute)
	var held *lease.HeldError
	if !errors.As(err, &held) || held.Lease.Holder != fabric {
		t.Fatalf("conflict %v", err)
	}
	if hs, _ := m.Holders(ctx, []string{"sw0", "sw1"}); len(hs) != 1 {
		t.Errorf("partial acquire not rolled back: %+v", hs)
	}
	again, err := m.Acquire(ctx, []string{"sw1"}, fabric, 10*time.Minute)
	if err != nil || again.Leases[0].Token != set.Leases[0].Token {
		t.Errorf("re-entry by the same holder: %v", err)
	}
	clock.Store(now.Add(11 * time.Minute).UnixNano())
	took, err := m.Acquire(ctx, []string{"sw1"}, wlan, time.Minute)
	if err != nil {
		t.Fatalf("expired lease not taken over: %v", err)
	}
	if err := m.Release(ctx, set); err != nil {
		t.Fatal(err)
	}
	if hs, _ := m.Holders(ctx, []string{"sw1", "sw2"}); len(hs) != 1 || hs[0].Holder != wlan {
		t.Errorf("release removed another holder's lease: %+v", hs)
	}
	_ = took
}

func TestManager_concurrent(t *testing.T) {
	t.Parallel()
	st, _ := filelease.Open(t.TempDir())
	m := lease.NewManager(st, nil)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			h := lease.Holder{Tool: fmt.Sprint("tool", i)}
			if _, err := m.Acquire(t.Context(), []string{"core1"}, h, time.Minute); err == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d holders acquired one device", wins.Load())
	}
}
