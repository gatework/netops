package batch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"
)

// SchemaVersion is the version of the Summary document.
const SchemaVersion = 1

// MaxWorkers bounds concurrency.
const MaxWorkers = 500

// ErrInvalid reports invalid options or devices.
var ErrInvalid = errors.New("batch: invalid")

// State is the end state of one device.
type State string

// Device end states.
const (
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateSkipped   State = "skipped"
)

// Result is the outcome of one device. Value is kept even when the task
// failed, so a partial collection is not lost.
type Result[T any] struct {
	Device   device.Device  `json:"device"`
	State    State          `json:"state"`
	Value    T              `json:"-"`
	Err      error          `json:"-"`
	Codes    []failure.Code `json:"codes,omitempty"`
	Started  time.Time      `json:"started,omitzero"`
	Duration time.Duration  `json:"duration"`
	// Persisted reports whether the Sink accepted the result.
	Persisted bool `json:"persisted"`
}

// Events observe progress. Callbacks are invoked serially, never
// concurrently, so they may write to a terminal without locking.
type Events[T any] struct {
	Started  func(device.Device)
	Finished func(Result[T])
}

// Options configure Run.
type Options[T any] struct {
	// Task does the work for one device. It must honor ctx.
	Task func(ctx context.Context, d device.Device) (T, error)
	// Workers bounds concurrency: 0 means 8, at most MaxWorkers.
	Workers int
	// Timeout bounds each device; zero means no per-device bound.
	Timeout time.Duration
	// Sink persists each finished result; it is called serially. A Sink
	// error cancels the devices not yet started.
	Sink func(ctx context.Context, r Result[T]) error
	// Classify maps task errors to codes; nil uses failure.Classify.
	Classify func(error) []failure.Code
	Events   Events[T]
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Report accounts for every requested device, ordered by device.
type Report[T any] struct {
	Results     []Result[T]
	Interrupted bool
	// SinkErr is the first Sink failure.
	SinkErr error
}

// Run executes o.Task for every device. The error reports invalid input,
// cancellation or a Sink failure; per-device failures are in the report.
func Run[T any](ctx context.Context, devices []device.Device, o Options[T]) (Report[T], error) {
	if err := validate(devices, &o); err != nil {
		return Report[T]{}, err
	}
	ordered := slices.Clone(devices)
	slices.SortStableFunc(ordered, device.Compare)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &runner[T]{o: o, cancel: cancel}
	rep := Report[T]{Results: make([]Result[T], len(ordered))}
	var g errgroup.Group
	g.SetLimit(o.Workers)
	for i, d := range ordered {
		rep.Results[i] = Result[T]{Device: d.Clone(), State: StateSkipped}
		// Go blocks until a worker is free; a device not started before
		// cancellation stays skipped.
		if runCtx.Err() != nil {
			continue
		}
		g.Go(func() error {
			// A device whose turn comes after cancellation stays skipped.
			if runCtx.Err() == nil {
				rep.Results[i] = r.device(runCtx, d)
			}
			return nil
		})
	}
	_ = g.Wait() // workers never return errors; failures are per device
	rep.SinkErr = r.sinkErr
	rep.Interrupted = ctx.Err() != nil
	switch {
	case r.sinkErr != nil:
		return rep, fmt.Errorf("batch: persist result: %w", r.sinkErr)
	case ctx.Err() != nil:
		return rep, ctx.Err()
	}
	return rep, nil
}

func validate[T any](devices []device.Device, o *Options[T]) error {
	if o.Task == nil {
		return fmt.Errorf("%w: nil task", ErrInvalid)
	}
	if o.Workers == 0 {
		o.Workers = 8
	}
	if o.Workers < 1 || o.Workers > MaxWorkers || o.Timeout < 0 {
		return fmt.Errorf("%w: workers %d timeout %s", ErrInvalid, o.Workers, o.Timeout)
	}
	if o.Classify == nil {
		o.Classify = failure.Classify
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ids := make(map[string]bool, len(devices))
	for _, d := range devices {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if ids[d.ID] {
			return fmt.Errorf("%w: duplicate device %s", ErrInvalid, d.ID)
		}
		ids[d.ID] = true
	}
	return nil
}

type runner[T any] struct {
	o      Options[T]
	cancel context.CancelFunc

	mu      sync.Mutex // serializes Sink and Events, guards sinkErr
	sinkErr error
}

func (r *runner[T]) device(ctx context.Context, d device.Device) Result[T] {
	res := Result[T]{Device: d.Clone(), Started: r.o.Now().UTC()}
	r.mu.Lock()
	if r.o.Events.Started != nil {
		r.o.Events.Started(d.Clone())
	}
	r.mu.Unlock()
	taskCtx := ctx
	if r.o.Timeout > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(ctx, r.o.Timeout)
		defer cancel()
	}
	res.Value, res.Err = r.o.Task(taskCtx, d.Clone())
	res.Duration = r.o.Now().Sub(res.Started)
	res.State = StateSucceeded
	if res.Err != nil {
		res.State, res.Codes = StateFailed, r.o.Classify(res.Err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.o.Sink != nil {
		if err := r.o.Sink(context.WithoutCancel(ctx), res); err != nil {
			if r.sinkErr == nil {
				r.sinkErr = err
			}
			r.cancel()
		} else {
			res.Persisted = true
		}
	}
	if r.o.Events.Finished != nil {
		r.o.Events.Finished(res)
	}
	return res
}

// DeviceSummary is the serializable outcome of one device.
type DeviceSummary struct {
	Device     device.Device  `json:"device"`
	State      State          `json:"state"`
	Codes      []failure.Code `json:"codes,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Persisted  bool           `json:"persisted"`
}

// Summary is the serializable account of a run.
type Summary struct {
	Schema      int             `json:"schema"`
	Requested   int             `json:"requested"`
	Counts      map[State]int   `json:"counts"`
	Interrupted bool            `json:"interrupted"`
	Devices     []DeviceSummary `json:"devices"`
}

// Summary returns the serializable account of the report.
func (r Report[T]) Summary() Summary {
	s := Summary{Schema: SchemaVersion, Requested: len(r.Results), Counts: map[State]int{}, Interrupted: r.Interrupted}
	s.Devices = make([]DeviceSummary, len(r.Results))
	for i, res := range r.Results {
		s.Counts[res.State]++
		s.Devices[i] = DeviceSummary{
			Device: res.Device, State: res.State, Codes: slices.Clone(res.Codes),
			DurationMS: res.Duration.Milliseconds(), Persisted: res.Persisted,
		}
	}
	return s
}

// Succeeded returns the results that succeeded.
func (r Report[T]) Succeeded() []Result[T] {
	return slices.DeleteFunc(slices.Clone(r.Results), func(x Result[T]) bool { return x.State != StateSucceeded })
}

// Failed reports whether any device failed or was skipped.
func (r Report[T]) Failed() bool {
	return slices.ContainsFunc(r.Results, func(x Result[T]) bool { return x.State != StateSucceeded })
}
