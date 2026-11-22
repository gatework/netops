package runs_test

import (
	"testing"
	"time"

	"github.com/gatework/netops/job"
	"github.com/gatework/netops/store/runs"
)

func TestHistory(t *testing.T) {
	t.Parallel()
	h, err := runs.Open(t.TempDir(), 3)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		r := job.Run{Job: "backup", Started: t0.Add(time.Duration(i) * time.Hour), Status: job.StatusSucceeded}
		if err := h.Record(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.Last(t.Context(), "backup", 2)
	if err != nil || len(got) != 2 || !got[1].Started.Equal(t0.Add(4*time.Hour)) {
		t.Errorf("last %+v %v", got, err)
	}
	all, _ := h.Last(t.Context(), "backup", 0)
	if len(all) != 3 {
		t.Errorf("kept %d, want 3", len(all))
	}
	if none, err := h.Last(t.Context(), "nightly", 5); err != nil || len(none) != 0 {
		t.Errorf("unknown job: %v %v", none, err)
	}
}
