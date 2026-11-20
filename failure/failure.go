package failure

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Code names one failure class. The zero value is CodeUnset.
type Code string

// The failure classes. The set is closed; decoding rejects other values.
const (
	CodeUnset       Code = ""
	CodeCancelled   Code = "cancelled"      // the operator or caller cancelled
	CodeTimeout     Code = "timeout"        // a deadline expired
	CodeAuth        Code = "auth_failed"    // credentials were refused
	CodeLocked      Code = "locked"         // the account is locked; never retry
	CodeUnreachable Code = "unreachable"    // the transport could not connect
	CodeHostKey     Code = "host_key"       // the SSH host key was not trusted
	CodeClosed      Code = "session_closed" // the session ended underneath the operation
	CodeBusy        Code = "session_busy"   // another operation owns the session
	CodeRejected    Code = "rejected"       // the device refused a command
	CodeUnconfirmed Code = "unconfirmed"    // a write or save was not confirmed
	CodeInteraction Code = "interaction"    // the device asked an unanticipated question
	CodeLimit       Code = "limit_exceeded" // an output or size bound was hit
	CodeIncomplete  Code = "incomplete"     // the response ended before completing
	CodeInvalid     Code = "invalid_input"  // a request failed validation
	CodeUnsupported Code = "unsupported"    // the platform lacks the capability
	CodeNoCreds     Code = "no_credentials" // no credentials are configured
	CodeDrifted     Code = "drifted"        // the device changed since planning
	CodeStorage     Code = "storage_failed" // local persistence failed
	CodeProtocol    Code = "protocol"       // a component broke its contract
	CodeInternal    Code = "internal"       // an unclassified failure
)

var known = []Code{
	CodeCancelled, CodeTimeout, CodeAuth, CodeLocked, CodeUnreachable, CodeHostKey, CodeClosed, CodeBusy,
	CodeRejected, CodeUnconfirmed, CodeInteraction, CodeLimit, CodeIncomplete, CodeInvalid, CodeUnsupported, CodeNoCreds,
	CodeDrifted, CodeStorage, CodeProtocol, CodeInternal,
}

// Codes returns every known code in declaration order.
func Codes() []Code { return slices.Clone(known) }

// Valid reports whether c is a known, set code.
func (c Code) Valid() bool { return slices.Contains(known, c) }

// String returns the wire form of the code.
func (c Code) String() string { return string(c) }

// MarshalText encodes a valid code.
func (c Code) MarshalText() ([]byte, error) {
	if c != CodeUnset && !c.Valid() {
		return nil, fmt.Errorf("failure: unknown code %q", string(c))
	}
	return []byte(c), nil
}

// UnmarshalText decodes a known code and rejects any other value.
func (c *Code) UnmarshalText(b []byte) error {
	v := Code(b)
	if v != CodeUnset && !v.Valid() {
		return fmt.Errorf("failure: unknown code %q", string(b))
	}
	*c = v
	return nil
}

// Coded is implemented by errors that carry a failure code.
type Coded interface {
	FailureCode() Code
}

// Error attaches a Code to a cause. Its message contains only the code and
// the operation, never the cause text, so it is safe to log and report.
type Error struct {
	Code Code
	Op   string // a short, static operation name such as "connect"
	// Reason is a stable, static refinement of Code, such as the name of
	// the device signature that matched ("invalid_input", "flash_full").
	// It never contains device output.
	Reason string
	err    error
}

// New returns an Error for code and operation without a cause.
func New(code Code, op string) *Error { return &Error{Code: code, Op: op} }

// Wrap returns an Error for code and operation that unwraps to cause.
func Wrap(code Code, op string, cause error) *Error { return &Error{Code: code, Op: op, err: cause} }

// Because returns e with Reason set.
func (e *Error) Because(reason string) *Error { e.Reason = reason; return e }

// Error returns "op: code" or "op: code (reason)".
func (e *Error) Error() string {
	s := string(e.Code)
	if e.Reason != "" {
		s += " (" + e.Reason + ")"
	}
	if e.Op == "" {
		return s
	}
	return e.Op + ": " + s
}

// Reason returns the Reason of the outermost coded Error in err's chain.
func Reason(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Reason
	}
	return ""
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.err }

// FailureCode returns the attached code.
func (e *Error) FailureCode() Code { return e.Code }

// Classify returns the distinct codes found in err's chain, outermost first.
// Coded errors stop the descent into their own causes. Context errors map to
// CodeCancelled and CodeTimeout. A branch that carries no code contributes
// CodeInternal, so an unknown failure is never reported as a device failure.
// A nil error yields nil.
func Classify(err error) []Code {
	var codes []Code
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if c := direct(err); c != CodeUnset {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
			return
		}
		switch w := err.(type) { //nolint:errorlint // walks the chain explicitly to visit every joined branch
		case interface{ Unwrap() []error }:
			for _, cause := range w.Unwrap() {
				visit(cause)
			}
			return
		case interface{ Unwrap() error }:
			if cause := w.Unwrap(); cause != nil {
				visit(cause)
				return
			}
		}
		if !slices.Contains(codes, CodeInternal) {
			codes = append(codes, CodeInternal)
		}
	}
	visit(err)
	return codes
}

// Primary returns the first code of Classify, or CodeUnset for nil.
func Primary(err error) Code {
	if codes := Classify(err); len(codes) > 0 {
		return codes[0]
	}
	return CodeUnset
}

// Cancelled reports whether err was caused by cancellation rather than a
// deadline. A timed-out device is a failure; a cancelled run is an
// interruption.
func Cancelled(err error) bool {
	return errors.Is(err, context.Canceled) || slices.Contains(Classify(err), CodeCancelled)
}

func direct(err error) Code {
	if c, ok := err.(Coded); ok {
		if code := c.FailureCode(); code != CodeUnset {
			return code
		}
	}
	switch err { //nolint:errorlint // sentinel identity on the current link; Classify walks the chain
	case context.Canceled:
		return CodeCancelled
	case context.DeadlineExceeded:
		return CodeTimeout
	}
	return CodeUnset
}
