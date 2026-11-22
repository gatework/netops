package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/failure"
)

// Status is how a run ended.
type Status string

// Run statuses.
const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Run is the record of one execution. Summary comes from the job and must
// not contain device output.
type Run struct {
	Job      string         `json:"job"`
	Started  time.Time      `json:"started"`
	Finished time.Time      `json:"finished"`
	Status   Status         `json:"status"`
	Codes    []failure.Code `json:"codes,omitempty"`
	Summary  string         `json:"summary,omitempty"`
}

// Duration returns how long the run took.
func (r Run) Duration() time.Duration { return r.Finished.Sub(r.Started) }

// History persists runs.
type History interface {
	Record(ctx context.Context, r Run) error
	Last(ctx context.Context, job string, n int) ([]Run, error)
}

// Job is one scheduled operation. Run returns a one-line summary.
type Job struct {
	Name    string
	Spec    Spec
	Timeout time.Duration // zero means no bound
	Run     func(ctx context.Context) (summary string, err error)
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// Options configure a Scheduler.
type Options struct {
	History History
	Events  event.Publisher
	Source  string // event source, the tool name
	Logger  *slog.Logger
	Now     func() time.Time
}

// Scheduler runs jobs on their specs.
type Scheduler struct {
	jobs []Job
	o    Options
}

// NewScheduler validates the jobs.
func NewScheduler(jobs []Job, o Options) (*Scheduler, error) {
	seen := map[string]bool{}
	for _, j := range jobs {
		if !namePattern.MatchString(j.Name) || seen[j.Name] || j.Run == nil || j.Timeout < 0 {
			return nil, fmt.Errorf("%w: job %q", ErrInvalid, j.Name)
		}
		if j.Spec.every == 0 && j.Spec.cron == nil {
			return nil, fmt.Errorf("%w: job %q has no schedule", ErrInvalid, j.Name)
		}
		seen[j.Name] = true
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Scheduler{jobs: slices.Clone(jobs), o: o}, nil
}

// Run blocks until ctx is cancelled, running every job on its schedule. It
// waits for runs in progress to observe the cancellation before returning.
func (s *Scheduler) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, j := range s.jobs {
		wg.Go(func() { s.loop(ctx, j) })
	}
	wg.Wait()
	return ctx.Err()
}

// RunNow executes j once, records the run and publishes a failure event.
// It returns the record.
func (s *Scheduler) RunNow(ctx context.Context, j Job) Run {
	r := Run{Job: j.Name, Started: s.o.Now().UTC()}
	runCtx := ctx
	if j.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, j.Timeout)
		defer cancel()
	}
	summary, err := j.Run(runCtx)
	r.Finished, r.Summary = s.o.Now().UTC(), summary
	switch {
	case err == nil:
		r.Status = StatusSucceeded
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		r.Status, r.Codes = StatusCancelled, []failure.Code{failure.CodeCancelled}
	default:
		r.Status, r.Codes = StatusFailed, failure.Classify(err)
	}
	persist := context.WithoutCancel(ctx)
	if s.o.History != nil {
		if herr := s.o.History.Record(persist, r); herr != nil {
			s.o.Logger.WarnContext(ctx, "job history not recorded", slog.String("job", j.Name), slog.Any("error", herr))
		}
	}
	if r.Status == StatusFailed {
		e, eerr := event.New(event.Event{
			Kind: event.KindJobFailed, Severity: assess.SeverityMedium, At: r.Finished, Source: s.o.Source,
			Subject: "job:" + j.Name, Summary: "scheduled job " + j.Name + " failed",
			Fields: event.Fields("code", string(failure.Primary(err))),
		})
		if eerr == nil {
			if perr := event.Emit(persist, s.o.Events, e); perr != nil {
				s.o.Logger.WarnContext(ctx, "job failure event not delivered", slog.String("job", j.Name), slog.Any("error", perr))
			}
		}
	}
	return r
}

func (s *Scheduler) loop(ctx context.Context, j Job) {
	for {
		next := j.Spec.Next(s.o.Now())
		if next.IsZero() {
			return
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.RunNow(ctx, j)
	}
}
