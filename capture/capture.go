package capture

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/parse"
)

// Output is the response to one command.
type Output struct {
	Command string `json:"command"`
	// Raw is the response as received, possibly including the command echo,
	// carriage returns and the closing prompt. It is empty for a command
	// that failed before answering.
	Raw string `json:"-"`
	// Prompt is the device prompt observed with this response, if known.
	Prompt string `json:"prompt,omitempty"`
	// RecordedAt is when the response completed.
	RecordedAt time.Time `json:"recorded_at"`
	// Duration is how long the command took.
	Duration time.Duration `json:"duration,omitempty"`
	// Failure is set when the command failed; Raw then holds whatever partial
	// response arrived and must not be read as complete.
	Failure failure.Code `json:"failure,omitempty"`
}

// OK reports whether the command answered without failure.
func (o Output) OK() bool { return o.Failure == failure.CodeUnset }

// Digest returns the digest of the raw response.
func (o Output) Digest() evidence.Digest { return evidence.DigestString(o.Raw) }

// Ref returns a source reference to this output on the named device.
func (o Output) Ref(device string) evidence.SourceRef {
	return evidence.SourceRef{Device: device, Command: o.Command, Digest: o.Digest()}
}

// Normalized returns Raw with NUL and carriage-return bytes removed, so
// that lines end in LF only. Body numbers lines of this text.
func (o Output) Normalized() string {
	return strings.NewReplacer("\r", "", "\x00", "").Replace(o.Raw)
}

// Body returns the response lines between the command echo and the closing
// prompt. The first line is dropped when it echoes the command; trailing
// blank lines and lines for which isPrompt returns true are dropped. Line
// numbers are those of Normalized. isPrompt may be nil, in which case only
// the recorded Prompt is recognized.
func (o Output) Body(ctx context.Context, isPrompt func(string) bool) ([]parse.Line, error) {
	content, err := parse.New(ctx, o.Normalized(), parse.Limits{})
	if err != nil {
		return nil, err
	}
	lines := content.Lines()
	if len(lines) > 0 && strings.TrimSpace(lines[0].Text) == strings.TrimSpace(o.Command) {
		lines = lines[1:]
	}
	prompt := strings.TrimSpace(strings.ReplaceAll(o.Prompt, "\x00", ""))
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1].Text)
		if last != "" && last != prompt && (isPrompt == nil || !isPrompt(last)) {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// Capture is every output collected from one device.
type Capture struct {
	Host    netip.Addr `json:"host"`
	Prompt  string     `json:"prompt,omitempty"`
	Outputs []Output   `json:"outputs"`
}

// Output returns the output for command. When a command appears more than
// once, the last one wins.
func (c Capture) Output(command string) (Output, bool) {
	for _, o := range slices.Backward(c.Outputs) {
		if o.Command == command {
			return o, true
		}
	}
	return Output{}, false
}

// Answer returns the output for command only when it succeeded.
func (c Capture) Answer(command string) (Output, bool) {
	o, ok := c.Output(command)
	return o, ok && o.OK()
}

// Failures returns the outputs that failed.
func (c Capture) Failures() []Output {
	var out []Output
	for _, o := range c.Outputs {
		if !o.OK() {
			out = append(out, o)
		}
	}
	return out
}

// RecordedAt returns the latest output time, or the zero time.
func (c Capture) RecordedAt() time.Time {
	var latest time.Time
	for _, o := range c.Outputs {
		if o.RecordedAt.After(latest) {
			latest = o.RecordedAt
		}
	}
	return latest
}

// Empty reports whether the capture holds no output.
func (c Capture) Empty() bool { return len(c.Outputs) == 0 }

// Merge combines c with a newer capture of the same device. For each
// command it keeps the most recent successful answer; a failure is kept
// only when no successful answer exists, and then the most recent failure.
// Command order follows first appearance, older capture first. The newer
// prompt wins when set. Neither input is modified.
func (c Capture) Merge(newer Capture) Capture {
	out := Capture{Host: c.Host, Prompt: c.Prompt}
	if newer.Host.IsValid() {
		out.Host = newer.Host
	}
	if newer.Prompt != "" {
		out.Prompt = newer.Prompt
	}
	best := map[string]Output{}
	var order []string
	for _, o := range slices.Concat(c.Outputs, newer.Outputs) {
		prev, seen := best[o.Command]
		if !seen {
			order = append(order, o.Command)
			best[o.Command] = o
			continue
		}
		if better(o, prev) {
			best[o.Command] = o
		}
	}
	out.Outputs = make([]Output, 0, len(order))
	for _, cmd := range order {
		out.Outputs = append(out.Outputs, best[cmd])
	}
	return out
}

// better reports whether o should replace prev for the same command.
func better(o, prev Output) bool {
	switch {
	case o.OK() != prev.OK():
		return o.OK()
	default:
		return !o.RecordedAt.Before(prev.RecordedAt)
	}
}

// PromptLike returns a matcher for the usual CLI prompt shapes: "<name>",
// "[name]", "name#" and "name>", optionally with a "(config...)" mode
// suffix. Vendors with other prompts pass their own matcher to Body.
func PromptLike() func(string) bool {
	return func(line string) bool {
		line = strings.TrimSpace(line)
		if len(line) < 2 || strings.ContainsAny(line, " \t") && !strings.Contains(line, "(") {
			return false
		}
		switch {
		case line[0] == '<' && line[len(line)-1] == '>', line[0] == '[' && line[len(line)-1] == ']':
			return true
		case strings.HasSuffix(line, "#"), strings.HasSuffix(line, ">"):
			return !strings.ContainsAny(line[:len(line)-1], "#> ")
		}
		return false
	}
}
