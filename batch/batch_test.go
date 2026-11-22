package batch_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"
)

func fleet(n int) []device.Device {
	out := make([]device.Device, n)
	for i := range n {
		out[n-1-i] = device.Device{ID: fmt.Sprint("d", i), Mgmt: netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)})}
	}
	return out
}

func TestRun(t *testing.T) {
	t.Parallel()
	rep, err := batch.Run(t.Context(), fleet(5), batch.Options[int]{
		Workers: 3, Timeout: time.Second,
		Task: func(ctx context.Context, d device.Device) (int, error) {
			if d.ID == "d2" {
				return 7, failure.New(failure.CodeAuth, "login")
			}
			return 1, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := rep.Summary()
	if s.Counts[batch.StateSucceeded] != 4 || s.Counts[batch.StateFailed] != 1 || s.Devices[0].Device.ID != "d0" {
		t.Errorf("summary %+v", s)
	}
	if r := rep.Results[2]; r.Value != 7 || r.Codes[0] != failure.CodeAuth {
		t.Errorf("partial value or codes lost: %+v", r)
	}
}

func TestRun_timeoutAndSinkFailure(t *testing.T) {
	t.Parallel()
	rep, err := batch.Run(t.Context(), fleet(4), batch.Options[int]{
		Workers: 1, Timeout: 10 * time.Millisecond,
		Task: func(ctx context.Context, d device.Device) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
		Sink: func(context.Context, batch.Result[int]) error { return errors.New("disk full") },
	})
	if err == nil || rep.SinkErr == nil {
		t.Fatal("sink failure not reported")
	}
	if rep.Results[0].Codes[0] != failure.CodeTimeout || rep.Results[3].State != batch.StateSkipped {
		t.Errorf("results %+v", rep.Results)
	}
}

func TestRun_rejectsDuplicates(t *testing.T) {
	t.Parallel()
	d := fleet(1)
	if _, err := batch.Run(t.Context(), append(d, d...), batch.Options[int]{Task: func(context.Context, device.Device) (int, error) { return 0, nil }}); !errors.Is(err, batch.ErrInvalid) {
		t.Errorf("duplicates: %v", err)
	}
}

func ExampleRun() {
	rep, _ := batch.Run(context.Background(), fleet(3), batch.Options[string]{
		Task: func(_ context.Context, d device.Device) (string, error) { return d.ID, nil },
	})
	for _, r := range rep.Results {
		fmt.Println(r.Device.Mgmt, r.State, r.Value)
	}
	// Output:
	// 192.0.2.1 succeeded d0
	// 192.0.2.2 succeeded d1
	// 192.0.2.3 succeeded d2
}

func TestRun_retryAndPartition(t *testing.T) {
	t.Parallel()
	ds := fleet(6)
	for i := range ds {
		ds[i].Site = fmt.Sprint("s", i%2)
	}
	var mu sync.Mutex
	running, peak := map[string]int{}, 0
	tries := map[string]int{}
	rep, err := batch.Run(t.Context(), ds, batch.Options[int]{
		Workers: 6, Partition: func(d device.Device) string { return d.Site }, PartitionLimit: 1,
		Retry: &batch.Retry{Attempts: 3, Backoff: time.Millisecond},
		Task: func(ctx context.Context, d device.Device) (int, error) {
			mu.Lock()
			running[d.Site]++
			peak = max(peak, running[d.Site])
			tries[d.ID]++
			n := tries[d.ID]
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			running[d.Site]--
			mu.Unlock()
			switch {
			case d.ID == "d1" && n < 3:
				return 0, failure.New(failure.CodeTimeout, "read")
			case d.ID == "d2":
				return 0, failure.New(failure.CodeAuth, "login")
			}
			return n, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if peak != 1 {
		t.Errorf("partition limit exceeded: %d at once", peak)
	}
	byID := map[string]batch.Result[int]{}
	for _, r := range rep.Results {
		byID[r.Device.ID] = r
	}
	if r := byID["d1"]; r.State != batch.StateSucceeded || r.Attempts != 3 {
		t.Errorf("transient failure not retried: %+v", r)
	}
	if r := byID["d2"]; r.State != batch.StateFailed || r.Attempts != 1 {
		t.Errorf("auth failure retried: %+v", r)
	}
	if rep.Results[0].Device.ID != "d0" {
		t.Error("results not in device order")
	}
}
