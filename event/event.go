package event

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

// Kind names an event type, dot-separated lowercase segments.
type Kind string

// Well-known kinds. Tools may define others with the same syntax.
const (
	KindConfigChanged       Kind = "config.changed"       // a backup found a new configuration version
	KindConfigOutOfSync     Kind = "config.out_of_sync"   // running differs from the archived configuration
	KindComplianceViolation Kind = "compliance.violation" // a rule or baseline failed
	KindCollectFailed       Kind = "collect.failed"       // a device could not be collected
	KindChangeExecuted      Kind = "change.executed"      // a plan was verified on a device
	KindChangeFailed        Kind = "change.failed"        // a plan failed before anything was sent
	KindChangeUncertain     Kind = "change.uncertain"     // a plan may have been sent; verify by hand
	KindChangeRolledBack    Kind = "change.rolled_back"   // a change was undone after a failed gate
	KindLifecycleViolation  Kind = "lifecycle.violation"  // software outside the version policy
	KindLeaseConflict       Kind = "lease.conflict"       // a device is held by another change
	KindJobFailed           Kind = "job.failed"           // a scheduled job failed
)

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Valid reports whether k is well formed.
func (k Kind) Valid() bool { return kindPattern.MatchString(string(k)) }

// ErrInvalid reports a malformed event.
var ErrInvalid = errors.New("event: invalid")

// Event is one operational event. Summary is a one-line operator text and
// must not contain device output or secrets.
type Event struct {
	ID       string               `json:"id"`
	Kind     Kind                 `json:"kind"`
	Severity assess.Severity      `json:"severity"`
	At       time.Time            `json:"at"`
	Source   string               `json:"source,omitempty"`
	Device   string               `json:"device,omitempty"`
	Subject  string               `json:"subject,omitempty"`
	Summary  string               `json:"summary"`
	Fields   []evidence.Field     `json:"fields,omitempty"`
	Refs     []evidence.SourceRef `json:"refs,omitempty"`
}

// New validates an event and derives its ID from kind, device, subject,
// time and summary.
func New(e Event) (Event, error) {
	if !e.Kind.Valid() || !e.Severity.Valid() || e.At.IsZero() || e.Summary == "" {
		return Event{}, fmt.Errorf("%w: kind %q severity %v", ErrInvalid, e.Kind, e.Severity)
	}
	e.At = e.At.UTC()
	e.Fields = slices.Clone(e.Fields)
	e.Refs = slices.Clone(e.Refs)
	e.ID = evidence.NewHasher("netops/event/v1").
		String("kind", string(e.Kind)).String("device", e.Device).String("subject", e.Subject).
		Int("at", e.At.UnixNano()).String("summary", e.Summary).Sum().Short()
	return e, nil
}

// Field returns the value of the named field.
func (e Event) Field(key string) (string, bool) {
	for _, f := range e.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// Fields is a helper for building event fields from pairs.
func Fields(kv ...string) []evidence.Field {
	out := make([]evidence.Field, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, evidence.Field{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

// Int formats an integer field value.
func Int(n int) string { return strconv.Itoa(n) }

// Sink delivers events somewhere: a file, a webhook, a chat channel.
type Sink interface {
	Deliver(ctx context.Context, events []Event) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, events []Event) error

// Deliver calls f.
func (f SinkFunc) Deliver(ctx context.Context, events []Event) error { return f(ctx, events) }

// Publisher is what operations need to emit events. *Bus implements it; a
// nil Publisher discards.
type Publisher interface {
	Publish(ctx context.Context, events ...Event) error
}

// Filter selects events. The zero Filter selects everything.
type Filter struct {
	Kinds       []Kind
	MinSeverity assess.Severity
	Devices     device.Selector
	// Inventory resolves device IDs for the Devices selector; events of
	// unknown devices pass only when Devices is empty.
	Inventory *device.Inventory
}

// Match reports whether f selects e.
func (f Filter) Match(e Event) bool {
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, e.Kind) {
		return false
	}
	if f.MinSeverity.Valid() && e.Severity < f.MinSeverity {
		return false
	}
	if f.Devices.Empty() {
		return true
	}
	d, ok := f.Inventory.Lookup(e.Device)
	return ok && f.Devices.Match(d)
}

// Route is a sink with its filter.
type Route struct {
	Name   string
	Filter Filter
	Sink   Sink
}

// Bus fans events out to routes. It is immutable and safe for concurrent
// use.
type Bus struct {
	routes []Route
}

// NewBus returns a bus over the routes.
func NewBus(routes ...Route) (*Bus, error) {
	for i, r := range routes {
		if r.Sink == nil {
			return nil, fmt.Errorf("%w: route %d has no sink", ErrInvalid, i)
		}
	}
	return &Bus{routes: slices.Clone(routes)}, nil
}

// DeliveryError reports the routes that failed.
type DeliveryError struct {
	Failed map[string]error
}

func (e *DeliveryError) Error() string {
	names := make([]string, 0, len(e.Failed))
	for n := range e.Failed {
		names = append(names, n)
	}
	slices.Sort(names)
	return fmt.Sprintf("event: delivery failed on %v", names)
}

// Unwrap returns the route errors.
func (e *DeliveryError) Unwrap() []error {
	out := make([]error, 0, len(e.Failed))
	for _, err := range e.Failed {
		out = append(out, err)
	}
	return out
}

// Publish delivers events to every route whose filter selects them. Every
// route is attempted; failures are reported together. A nil Bus discards.
func (b *Bus) Publish(ctx context.Context, events ...Event) error {
	if b == nil || len(events) == 0 {
		return nil
	}
	failed := map[string]error{}
	for i, r := range b.routes {
		var picked []Event
		for _, e := range events {
			if r.Filter.Match(e) {
				picked = append(picked, e)
			}
		}
		if len(picked) == 0 {
			continue
		}
		if err := r.Sink.Deliver(ctx, picked); err != nil {
			name := r.Name
			if name == "" {
				name = "route-" + strconv.Itoa(i)
			}
			failed[name] = err
		}
	}
	if len(failed) > 0 {
		return &DeliveryError{Failed: failed}
	}
	return nil
}

// Emit publishes through p, treating a nil Publisher as a discard. It is
// the helper operations use so that events stay optional.
func Emit(ctx context.Context, p Publisher, events ...Event) error {
	if p == nil || len(events) == 0 {
		return nil
	}
	return p.Publish(ctx, events...)
}

// Memory is a Sink that keeps events, for tests and in-process consumers.
// The zero value is ready; it is safe for concurrent use.
type Memory struct {
	mu     sync.Mutex // guards events
	events []Event
}

// Deliver appends the events.
func (m *Memory) Deliver(_ context.Context, events []Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	return nil
}

// Events returns a copy of the delivered events.
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events)
}
