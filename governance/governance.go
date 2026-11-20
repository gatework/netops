package governance

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/evidence"
)

// ErrInvalid reports an invalid window or policy.
var ErrInvalid = errors.New("governance: invalid")

// Class is the change class.
type Class string

// Change classes.
const (
	ClassStandard  Class = "standard"  // pre-approved, low-risk, repeatable
	ClassNormal    Class = "normal"    // reviewed and approved
	ClassEmergency Class = "emergency" // urgent, justified, reviewed afterwards
)

// Valid reports whether c is a known class.
func (c Class) Valid() bool { return c == ClassStandard || c == ClassNormal || c == ClassEmergency }

// Window is a recurring maintenance window, such as "Sat,Sun 01:00-05:00"
// in a time zone. A window whose end is before its start spans midnight
// and belongs to the day it starts.
type Window struct {
	days       []time.Weekday // empty means every day
	start, end int            // minutes after midnight
	loc        *time.Location
	text       string
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseWindow parses "[Day,Day ]HH:MM-HH:MM" in loc (nil means UTC).
func ParseWindow(text string, loc *time.Location) (Window, error) {
	if loc == nil {
		loc = time.UTC
	}
	w := Window{loc: loc, text: strings.TrimSpace(text)}
	spec := w.text
	if days, rest, ok := strings.Cut(spec, " "); ok {
		for d := range strings.SplitSeq(days, ",") {
			name := strings.ToLower(strings.TrimSpace(d))
			if len(name) > 3 {
				name = name[:3]
			}
			wd, known := dayNames[name]
			if !known {
				return Window{}, fmt.Errorf("%w: window day %q", ErrInvalid, d)
			}
			w.days = append(w.days, wd)
		}
		spec = strings.TrimSpace(rest)
	}
	from, to, ok := strings.Cut(spec, "-")
	if !ok {
		return Window{}, fmt.Errorf("%w: window %q is not HH:MM-HH:MM", ErrInvalid, text)
	}
	var err error
	if w.start, err = clock(from); err != nil {
		return Window{}, err
	}
	if w.end, err = clock(to); err != nil {
		return Window{}, err
	}
	if w.start == w.end {
		return Window{}, fmt.Errorf("%w: empty window %q", ErrInvalid, text)
	}
	return w, nil
}

func clock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%w: time %q", ErrInvalid, s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether t falls inside the window.
func (w Window) Contains(t time.Time) bool {
	if w.loc == nil {
		return false
	}
	local := t.In(w.loc)
	m := local.Hour()*60 + local.Minute()
	day := local.Weekday()
	if w.start < w.end {
		return m >= w.start && m < w.end && w.onDay(day)
	}
	switch {
	case m >= w.start:
		return w.onDay(day)
	case m < w.end:
		return w.onDay((day + 6) % 7) // the window started yesterday
	}
	return false
}

// String returns the window as parsed, with its zone.
func (w Window) String() string {
	if w.loc == nil {
		return ""
	}
	return w.text + " " + w.loc.String()
}

func (w Window) onDay(d time.Weekday) bool { return len(w.days) == 0 || slices.Contains(w.days, d) }

// Freeze is a period during which normal changes are forbidden.
type Freeze struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Reason string    `json:"reason,omitempty"`
}

// Approval is one approver's consent to an exact plan digest.
type Approval struct {
	Plan   evidence.Digest `json:"plan"`
	By     string          `json:"by"`
	At     time.Time       `json:"at"`
	Ticket string          `json:"ticket,omitempty"`
}

// Policy is the change policy.
type Policy struct {
	// Windows allow normal changes; none means any time.
	Windows []Window
	Freezes []Freeze
	// RequireTicket refuses normal and emergency changes without a ticket.
	RequireTicket bool
	// Approvals is the number of distinct approvers required per class.
	Approvals map[Class]int
	// ApprovalTTL expires approvals; zero means they never expire.
	ApprovalTTL time.Duration
	// MaxDevices caps the devices a single change may touch per class;
	// zero means no cap.
	MaxDevices map[Class]int
}

// Request describes the change asking to execute.
type Request struct {
	Plan          evidence.Digest
	Class         Class
	Ticket        string
	Author        string
	Devices       int
	Approvals     []Approval
	Justification string // required for emergency bypasses
	Now           time.Time
}

// Reason is a stable refusal or bypass code.
type Reason string

// Reasons.
const (
	ReasonInvalidClass     Reason = "invalid_class"
	ReasonOutsideWindow    Reason = "outside_window"
	ReasonFrozen           Reason = "change_freeze"
	ReasonNoTicket         Reason = "no_ticket"
	ReasonApprovals        Reason = "insufficient_approvals"
	ReasonSelfApproval     Reason = "self_approval"
	ReasonApprovalExpired  Reason = "approval_expired"
	ReasonApprovalMismatch Reason = "approval_for_other_plan"
	ReasonTooManyDevices   Reason = "too_many_devices"
	ReasonNoJustification  Reason = "no_justification"
	ReasonWindowBypassed   Reason = "window_bypassed"
	ReasonFreezeBypassed   Reason = "freeze_bypassed"
)

// Decision is the outcome of Gate.
type Decision struct {
	Allowed  bool     `json:"allowed"`
	Refusals []Reason `json:"refusals,omitempty"`
	Bypassed []Reason `json:"bypassed,omitempty"`
	// Approvers are the distinct, valid approvers counted.
	Approvers []string `json:"approvers,omitempty"`
}

// Gate decides whether r may execute under p.
func Gate(p Policy, r Request) Decision {
	var d Decision
	refuse := func(reason Reason) {
		if !slices.Contains(d.Refusals, reason) {
			d.Refusals = append(d.Refusals, reason)
		}
	}
	if !r.Class.Valid() {
		refuse(ReasonInvalidClass)
		return d
	}
	emergency := r.Class == ClassEmergency
	if emergency && strings.TrimSpace(r.Justification) == "" {
		refuse(ReasonNoJustification)
	}
	if !inWindow(p.Windows, r.Now) && r.Class != ClassStandard {
		if emergency {
			d.Bypassed = append(d.Bypassed, ReasonWindowBypassed)
		} else {
			refuse(ReasonOutsideWindow)
		}
	}
	if frozen(p.Freezes, r.Now) {
		if emergency {
			d.Bypassed = append(d.Bypassed, ReasonFreezeBypassed)
		} else {
			refuse(ReasonFrozen)
		}
	}
	if p.RequireTicket && r.Class != ClassStandard && strings.TrimSpace(r.Ticket) == "" {
		refuse(ReasonNoTicket)
	}
	if limit := p.MaxDevices[r.Class]; limit > 0 && r.Devices > limit {
		refuse(ReasonTooManyDevices)
	}
	d.Approvers = approvers(p, r, refuse)
	if len(d.Approvers) < p.Approvals[r.Class] {
		refuse(ReasonApprovals)
	}
	d.Allowed = len(d.Refusals) == 0
	return d
}

func approvers(p Policy, r Request, refuse func(Reason)) []string {
	var out []string
	for _, a := range r.Approvals {
		switch {
		case a.Plan != r.Plan:
			refuse(ReasonApprovalMismatch)
		case a.By == "" || a.By == r.Author:
			refuse(ReasonSelfApproval)
		case p.ApprovalTTL > 0 && r.Now.Sub(a.At) > p.ApprovalTTL:
			refuse(ReasonApprovalExpired)
		case !slices.Contains(out, a.By):
			out = append(out, a.By)
		}
	}
	slices.Sort(out)
	return out
}

func inWindow(windows []Window, t time.Time) bool {
	return len(windows) == 0 || slices.ContainsFunc(windows, func(w Window) bool { return w.Contains(t) })
}

func frozen(freezes []Freeze, t time.Time) bool {
	return slices.ContainsFunc(freezes, func(f Freeze) bool { return !t.Before(f.From) && t.Before(f.To) })
}
