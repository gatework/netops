package change

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/secret"
)

var (
	// ErrInvalid reports an invalid change, plan or envelope.
	ErrInvalid = errors.New("change: invalid")
	// ErrSatisfied reports a change the model already satisfies; there is
	// nothing to plan.
	ErrSatisfied = errors.New("change: already satisfied")
	// ErrIneligible reports a change that cannot apply to the model.
	ErrIneligible = errors.New("change: not eligible")
	// ErrDrifted reports a device that changed since planning.
	ErrDrifted = errors.New("change: device drifted since planning")
	// ErrUnverified reports a change that was not observed after writing.
	ErrUnverified = errors.New("change: not verified")
	// ErrNoRollback reports a plan without rollback commands.
	ErrNoRollback = errors.New("change: no rollback")
)

// Kind names a type of change, such as "wlan.kick" or "vlan.add".
type Kind string

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$`)

// Valid reports whether k is well formed.
func (k Kind) Valid() bool { return kindPattern.MatchString(string(k)) }

// Command is one line sent to a device. A sensitive command carries a
// secret and is redacted in journals and reports.
type Command struct {
	Text      string `json:"text"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// Commands wraps non-sensitive command texts.
func Commands(texts ...string) []Command {
	out := make([]Command, len(texts))
	for i, t := range texts {
		out[i] = Command{Text: t}
	}
	return out
}

// Display returns the text, or a mask for a sensitive command.
func (c Command) Display() string {
	if c.Sensitive {
		return secret.Mask
	}
	return c.Text
}

func (c Command) validate() error {
	if strings.TrimSpace(c.Text) == "" || strings.ContainsAny(c.Text, "\r\n\x00") {
		return fmt.Errorf("%w: command text", ErrInvalid)
	}
	return nil
}

// Script is what realizes a change and what undoes it.
type Script struct {
	Commands []Command `json:"commands"`
	Rollback []Command `json:"rollback,omitempty"`
}

func (s Script) validate() error {
	if len(s.Commands) == 0 {
		return fmt.Errorf("%w: empty script", ErrInvalid)
	}
	for _, c := range slices.Concat(s.Commands, s.Rollback) {
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (s Script) clone() Script {
	return Script{Commands: slices.Clone(s.Commands), Rollback: slices.Clone(s.Rollback)}
}

// Change is one typed intent over a device model M. Implementations are
// immutable values that encode to JSON for envelopes.
type Change[M any] interface {
	// Kind names the change type for the codec.
	Kind() Kind
	// Validate checks the change on its own.
	Validate() error
	// Eligible reports why the change cannot apply to m, or nil.
	Eligible(m M) error
	// Satisfied reports whether m already realizes the change.
	Satisfied(m M) bool
	// Render returns the commands that realize the change on m.
	Render(m M) (Script, error)
}

// Preserving is implemented by changes that can tell whether anything
// beyond their own scope changed between two models. Verify uses it to
// catch collateral changes.
type Preserving[M any] interface {
	Preserved(before, after M) error
}

// Effect is implemented by imperative changes whose success is judged
// against the state before, rather than by a target state: disconnecting a
// client that may reconnect at once, clearing counters, bouncing a port.
// Verify then calls Effective instead of Satisfied.
type Effect[M any] interface {
	Effective(before, after M) bool
}

// Fingerprint identifies the state of a model for drift detection. It must
// cover everything a change may depend on.
type Fingerprint[M any] func(M) evidence.Digest

// Meta is the audit metadata of a plan.
type Meta struct {
	ID      string    `json:"id"`
	Ticket  string    `json:"ticket,omitempty"`
	Author  string    `json:"author,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Created time.Time `json:"created"`
}

var planIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (m Meta) validate() error {
	if !planIDPattern.MatchString(m.ID) {
		return fmt.Errorf("%w: plan id %q", ErrInvalid, m.ID)
	}
	if m.Created.IsZero() {
		return fmt.Errorf("%w: plan %s has no creation time", ErrInvalid, m.ID)
	}
	return nil
}
