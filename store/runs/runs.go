package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gatework/netops/job"
	"github.com/gatework/netops/store"
)

// DefaultKeep is the number of runs kept per job.
const DefaultKeep = 100

// History keeps job runs under a root.
type History struct {
	st   *store.Store
	keep int
}

var _ job.History = (*History)(nil)

// Open opens or creates the history root; keep <= 0 uses DefaultKeep.
func Open(root string, keep int) (*History, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	return &History{st: st, keep: keep}, nil
}

func rel(name string) string { return "runs/" + store.Name(name) + ".json" }

// Record appends a run and trims the oldest beyond the limit.
func (h *History) Record(ctx context.Context, r job.Run) error {
	lock, err := h.st.Lock(ctx, "runs/"+r.Job, 10*time.Second)
	if err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	defer lock.Close()
	runs, err := h.load(ctx, r.Job)
	if err != nil {
		return err
	}
	runs = append(runs, r)
	if len(runs) > h.keep {
		runs = runs[len(runs)-h.keep:]
	}
	if _, err := h.st.WriteJSON(ctx, rel(r.Job), runs); err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	return nil
}

// Last returns up to n most recent runs of job, newest last; n <= 0
// returns all kept runs.
func (h *History) Last(ctx context.Context, name string, n int) ([]job.Run, error) {
	runs, err := h.load(ctx, name)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(runs) > n {
		runs = runs[len(runs)-n:]
	}
	return runs, nil
}

func (h *History) load(ctx context.Context, name string) ([]job.Run, error) {
	var runs []job.Run
	err := h.st.ReadJSON(ctx, rel(name), 16<<20, &runs)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: %w", err)
	}
	return runs, nil
}
