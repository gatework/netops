package job_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gatework/netops/event"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/job"
)

func TestSpec_Next(t *testing.T) {
	t.Parallel()
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	base := time.Date(2026, 11, 21, 10, 7, 30, 0, shanghai) // Saturday
	tests := []struct {
		expr string
		want string
	}{
		{"@every 5m", "2026-11-21 10:12:30"},
		{"@hourly", "2026-11-21 11:00:00"},
		{"@daily", "2026-11-22 00:00:00"},
		{"30 2 * * 1-5", "2026-11-23 02:30:00"},
		{"*/15 * * * *", "2026-11-21 10:15:00"},
		{"0 9 1 * *", "2026-12-01 09:00:00"},
		{"0 0 13 * 5", "2026-11-27 00:00:00"}, // Friday or the 13th, whichever first
		{"0 0 * * 7", "2026-11-22 00:00:00"},  // 7 is Sunday
	}
	for _, tc := range tests {
		s, err := job.ParseSpec(tc.expr, shanghai)
		if err != nil {
			t.Fatal(tc.expr, err)
		}
		if got := s.Next(base).In(shanghai).Format(time.DateTime); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.expr, got, tc.want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "@every 10s", "5-1 * * * *", "*/0 * * * *", "0 0 31 2 *x"} {
		if _, err := job.ParseSpec(bad, nil); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
	never, _ := job.ParseSpec("0 0 31 2 *", nil)
	if !never.Next(base).IsZero() {
		t.Error("February 31 fired")
	}
}

type memHistory struct {
	mu   sync.Mutex
	runs []job.Run
}

func (h *memHistory) Record(_ context.Context, r job.Run) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = append(h.runs, r)
	return nil
}

func (h *memHistory) Last(context.Context, string, int) ([]job.Run, error) { return nil, nil }

func (h *memHistory) snapshot() []job.Run {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]job.Run(nil), h.runs...)
}

func TestScheduler(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spec, _ := job.ParseSpec("@every 10m", nil)
		h := &memHistory{}
		var sink event.Memory
		bus, _ := event.NewBus(event.Route{Sink: &sink})
		n := 0
		s, err := job.NewScheduler([]job.Job{{
			Name: "backup", Spec: spec,
			Run: func(ctx context.Context) (string, error) {
				n++
				time.Sleep(15 * time.Minute) // longer than the interval: no overlap
				if n == 2 {
					return "", failure.New(failure.CodeTimeout, "backup")
				}
				return "ok", nil
			},
		}}, job.Options{History: h, Events: bus, Source: "test"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		_ = s.Run(ctx)
		runs := h.snapshot()
		if len(runs) < 2 || runs[0].Status != job.StatusSucceeded || runs[1].Status != job.StatusFailed {
			t.Fatalf("runs %+v", runs)
		}
		for i := 1; i < len(runs); i++ {
			if runs[i].Started.Before(runs[i-1].Finished) {
				t.Error("runs overlap")
			}
		}
		if ev := sink.Events(); len(ev) != 1 || ev[0].Kind != event.KindJobFailed {
			t.Errorf("events %+v", ev)
		}
	})
}

func ExampleParseSpec() {
	s, _ := job.ParseSpec("30 2 * * 1-5", time.UTC)
	fmt.Println(s.Next(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)).Format(time.DateTime))
	// Output: 2026-11-23 02:30:00
}
