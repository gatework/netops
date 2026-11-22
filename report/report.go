package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/term"
)

// KV is one summary line.
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Table is a header and rows; short rows are padded with empty cells.
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Add appends a row.
func (t *Table) Add(cells ...string) { t.Rows = append(t.Rows, cells) }

// Section is one part of a document.
type Section struct {
	Title    string           `json:"title"`
	Text     []string         `json:"text,omitempty"`
	Table    *Table           `json:"table,omitempty"`
	Findings []assess.Finding `json:"findings,omitempty"`
}

// Document is a report.
type Document struct {
	Title     string    `json:"title"`
	Generated time.Time `json:"generated"`
	Summary   []KV      `json:"summary,omitempty"`
	Sections  []Section `json:"sections"`
}

// Columns of a findings table.
var findingColumns = []string{"severity", "rule", "subject", "detail"}

// Findings renders findings as a table: severity, rule, subject, detail
// (the title when there is no detail), in the given order.
func Findings(fs []assess.Finding) *Table {
	t := &Table{Columns: findingColumns}
	for _, f := range fs {
		detail := f.Detail
		if detail == "" {
			detail = f.Title
		}
		t.Add(f.Severity.String(), string(f.Rule), f.Subject.String(), detail)
	}
	return t
}

// cells returns row padded or cut to n columns.
func cells(row []string, n int) []string {
	out := make([]string, n)
	copy(out, row)
	return out
}

// Text renders the document for a terminal.
func Text(w io.Writer, d Document) error {
	var b strings.Builder
	b.WriteString(term.Sanitize(d.Title) + "\n")
	if !d.Generated.IsZero() {
		b.WriteString(d.Generated.Format(time.DateTime) + "\n")
	}
	for _, kv := range d.Summary {
		b.WriteString(term.Sanitize(kv.Key) + ": " + term.Sanitize(kv.Value) + "\n")
	}
	for _, s := range d.Sections {
		b.WriteString("\n" + term.Sanitize(s.Title) + "\n")
		for _, p := range s.Text {
			b.WriteString(term.Sanitize(p) + "\n")
		}
		for _, t := range s.tables() {
			tt := term.NewTable(t.Columns...)
			for _, r := range t.Rows {
				tt.Add(cells(r, len(t.Columns))...)
			}
			if _, err := tt.Render(&b, 0); err != nil {
				return fmt.Errorf("report: %w", err)
			}
		}
	}
	return write(w, b.String())
}

func (s Section) tables() []*Table {
	var out []*Table
	if s.Table != nil {
		out = append(out, s.Table)
	}
	if len(s.Findings) > 0 {
		out = append(out, Findings(s.Findings))
	}
	return out
}

// Markdown renders the document as GitHub-flavored Markdown.
func Markdown(w io.Writer, d Document) error {
	var b strings.Builder
	b.WriteString("# " + mdInline(d.Title) + "\n\n")
	if !d.Generated.IsZero() {
		b.WriteString("_" + d.Generated.Format(time.DateTime) + "_\n\n")
	}
	for _, kv := range d.Summary {
		b.WriteString("- **" + mdInline(kv.Key) + "**: " + mdInline(kv.Value) + "\n")
	}
	if len(d.Summary) > 0 {
		b.WriteString("\n")
	}
	for _, s := range d.Sections {
		b.WriteString("## " + mdInline(s.Title) + "\n\n")
		for _, p := range s.Text {
			b.WriteString(mdInline(p) + "\n\n")
		}
		for _, t := range s.tables() {
			b.WriteString("| " + strings.Join(mdCells(t.Columns), " | ") + " |\n")
			b.WriteString("|" + strings.Repeat(" --- |", len(t.Columns)) + "\n")
			for _, r := range t.Rows {
				b.WriteString("| " + strings.Join(mdCells(cells(r, len(t.Columns))), " | ") + " |\n")
			}
			b.WriteString("\n")
		}
	}
	return write(w, b.String())
}

func mdCells(cs []string) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = strings.ReplaceAll(mdInline(c), "|", `\|`)
	}
	return out
}

// mdInline collapses newlines and escapes characters that would start
// Markdown markup.
func mdInline(s string) string {
	s = strings.Join(strings.Fields(term.Sanitize(s)), " ")
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// CSV renders one table per RFC 4180, header first.
func CSV(w io.Writer, t *Table) error {
	if t == nil {
		return nil
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(t.Columns); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	for _, r := range t.Rows {
		row := cells(r, len(t.Columns))
		for i, c := range row {
			row[i] = csvSafe(c)
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("report: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}

// csvSafe neutralizes cells a spreadsheet would evaluate as formulas.
func csvSafe(c string) string {
	if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
		return "'" + c
	}
	return c
}

// JSON renders the document as indented JSON.
func JSON(w io.Writer, d Document) error { return term.WriteJSON(w, d) }

func write(w io.Writer, s string) error {
	if _, err := io.WriteString(w, s); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}

// MarshalJSON keeps an empty Sections list as [] for stable schemas.
func (d Document) MarshalJSON() ([]byte, error) {
	type plain Document
	if d.Sections == nil {
		d.Sections = []Section{}
	}
	return json.Marshal(plain(d))
}
