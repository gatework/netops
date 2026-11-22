package job

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalid reports an invalid spec or job.
var ErrInvalid = errors.New("job: invalid")

// Spec is a schedule. The zero Spec never fires.
type Spec struct {
	text  string
	every time.Duration
	cron  *cron
	loc   *time.Location
}

type cron struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domAny, dowAny                bool
}

// ParseSpec parses expr in loc (nil means UTC).
func ParseSpec(expr string, loc *time.Location) (Spec, error) {
	if loc == nil {
		loc = time.UTC
	}
	expr = strings.TrimSpace(expr)
	s := Spec{text: expr, loc: loc}
	switch expr {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	}
	if rest, ok := strings.CutPrefix(expr, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil || d < time.Minute {
			return Spec{}, fmt.Errorf("%w: %q needs a duration of at least 1m", ErrInvalid, s.text)
		}
		s.every = d
		return s, nil
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return Spec{}, fmt.Errorf("%w: %q is not a five-field cron expression", ErrInvalid, s.text)
	}
	c := &cron{domAny: f[2] == "*", dowAny: f[4] == "*"}
	var err error
	for i, spec := range []struct {
		dst       *uint64
		lo, hi    int
		wrapSeven bool
	}{{&c.minute, 0, 59, false}, {&c.hour, 0, 23, false}, {&c.dom, 1, 31, false}, {&c.month, 1, 12, false}, {&c.dow, 0, 7, true}} {
		if *spec.dst, err = field(f[i], spec.lo, spec.hi); err != nil {
			return Spec{}, fmt.Errorf("%w: %q field %d: %w", ErrInvalid, s.text, i+1, err)
		}
		if spec.wrapSeven && *spec.dst&(1<<7) != 0 {
			*spec.dst = *spec.dst&^(1<<7) | 1 // 7 is Sunday
		}
	}
	s.cron = c
	return s, nil
}

// field parses "*", "a", "a-b", lists and "/step".
func field(expr string, lo, hi int) (uint64, error) {
	var bits uint64
	for part := range strings.SplitSeq(expr, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step %q", stepText)
			}
			step = n
		}
		from, to := lo, hi
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if from, err = strconv.Atoi(a); err != nil {
				return 0, fmt.Errorf("bad value %q", a)
			}
			to = from
			if isRange {
				if to, err = strconv.Atoi(b); err != nil {
					return 0, fmt.Errorf("bad value %q", b)
				}
			} else if hasStep {
				to = hi
			}
		}
		if from < lo || to > hi || from > to {
			return 0, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

// String returns the expression as given.
func (s Spec) String() string { return s.text }

// Next returns the first firing strictly after t, or the zero time when
// the spec never fires within five years.
func (s Spec) Next(after time.Time) time.Time {
	switch {
	case s.every > 0:
		return after.Add(s.every).Truncate(time.Second)
	case s.cron == nil:
		return time.Time{}
	}
	c := s.cron
	t := after.In(s.loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<int(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, s.loc)
		case !c.dayMatch(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, s.loc)
		case c.hour&(1<<t.Hour()) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, s.loc)
		case c.minute&(1<<t.Minute()) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// dayMatch applies cron semantics: when both day fields are restricted, a
// day matches either.
func (c *cron) dayMatch(t time.Time) bool {
	dom := c.dom&(1<<t.Day()) != 0
	dow := c.dow&(1<<int(t.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}
