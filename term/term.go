package term

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/width"
)

// Exit codes shared by every tool.
const (
	ExitOK          = 0   // success
	ExitError       = 1   // failure, or a partial result
	ExitUsage       = 2   // invalid arguments or configuration
	ExitFindings    = 3   // the command ran and found problems at or above the threshold
	ExitInterrupted = 130 // the operator interrupted
)

// Sanitize escapes control and invisible format characters except newline
// and tab as \uXXXX.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, hidden) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if hidden(r) {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hidden(r rune) bool {
	return r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}

// Printf writes a formatted line with every string argument sanitized.
func Printf(w io.Writer, format string, args ...any) error {
	safe := make([]any, len(args))
	for i, a := range args {
		if s, ok := a.(string); ok {
			a = Sanitize(s)
		}
		safe[i] = a
	}
	_, err := fmt.Fprintf(w, format, safe...)
	return err
}

// Width returns the display width of s: two columns for wide and
// full-width runes, none for combining marks and controls.
func Width(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r) || unicode.IsControl(r):
		default:
			switch width.LookupRune(r).Kind() {
			case width.EastAsianWide, width.EastAsianFullwidth:
				n += 2
			case width.Neutral, width.EastAsianAmbiguous, width.EastAsianNarrow, width.EastAsianHalfwidth:
				n++
			}
		}
	}
	return n
}

// Table renders aligned columns. Cells are sanitized when added.
type Table struct {
	header []string
	rows   [][]string
}

// NewTable returns a table with a header row.
func NewTable(header ...string) *Table { return &Table{header: header} }

// Add appends a row; empty cells render as "-".
func (t *Table) Add(cells ...string) {
	row := make([]string, len(cells))
	for i, c := range cells {
		row[i] = Dash(Sanitize(c))
	}
	t.rows = append(t.rows, row)
}

// Len returns the number of rows.
func (t *Table) Len() int { return len(t.rows) }

// Render writes the table. When limit > 0 only the first limit rows are
// written; the return reports how many were left out.
func (t *Table) Render(w io.Writer, limit int) (omitted int, err error) {
	rows := t.rows
	if limit > 0 && len(rows) > limit {
		omitted = len(rows) - limit
		rows = rows[:limit]
	}
	all := append([][]string{t.header}, rows...)
	widths := make([]int, len(t.header))
	for _, row := range all {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], Width(cell))
			}
		}
	}
	var b strings.Builder
	for _, row := range all {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			b.WriteString(cell)
			if i < len(widths)-1 && i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-Width(cell)+2))
			}
		}
		b.WriteByte('\n')
	}
	_, err = io.WriteString(w, b.String())
	return omitted, err
}

// Dash returns "-" for blank s.
func Dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// Duration renders a duration compactly: 3d4h, 2h5m, 4m10s, 9s.
func Duration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(time.Second)
	days, hours := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
	minutes, seconds := int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// Time renders a local timestamp, or "-" for the zero time.
func Time(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.DateTime)
}

// ErrAborted reports a change the operator did not confirm.
var ErrAborted = errors.New("term: aborted by operator")

// Prompt is where a confirmation reads and writes.
type Prompt struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool
}

// Confirm asks the operator to type want exactly. With yes it succeeds
// without asking; without a terminal it refuses, so scripts must pass yes
// explicitly. message is printed before the prompt and should state what
// will change.
func (p Prompt) Confirm(message, want string, yes bool) error {
	if yes {
		return nil
	}
	if !p.Interactive {
		return fmt.Errorf("%w: confirmation needs a terminal or an explicit --yes", ErrAborted)
	}
	if err := Printf(p.Out, "%s\n> %s: ", message, want); err != nil {
		return fmt.Errorf("term: %w", err)
	}
	line, err := bufio.NewReader(p.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %w", ErrAborted, err)
	}
	if strings.TrimSpace(line) != want {
		return ErrAborted
	}
	return nil
}

// Document is the JSON envelope of every machine-readable output.
type Document struct {
	Schema    string    `json:"schema"`
	Generated time.Time `json:"generated"`
	// Freshness describes cached evidence used, when any.
	Freshness any `json:"freshness,omitempty"`
	Result    any `json:"result"`
}

// WriteJSON writes v as indented JSON without HTML escaping.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("term: %w", err)
	}
	return nil
}

// Messages maps stable codes (failure codes, governance reasons, rule
// states) to operator text in one language, falling back to the code
// itself.
type Messages map[string]string

// Text returns the message for code, or the code.
func (m Messages) Text(code string) string {
	if s, ok := m[code]; ok {
		return s
	}
	return code
}

// Join renders several codes with sep.
func (m Messages) Join(sep string, codes ...string) string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = m.Text(c)
	}
	return strings.Join(out, sep)
}
