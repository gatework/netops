package parse

import (
	"github.com/gatework/netops/evidence"
)

// IssueUnparsed is the issue code for lines no parser consumed.
const IssueUnparsed = "unparsed_line"

// Scanner accumulates the issues of decoding one command output and decides
// its status. Issues point at the output through the Scanner's SourceRef.
// The zero value is usable and records issues without a source.
type Scanner struct {
	source *evidence.SourceRef
	issues []evidence.Issue
	failed bool
}

// NewScanner returns a Scanner whose issues cite source.
func NewScanner(source evidence.SourceRef) *Scanner { return &Scanner{source: &source} }

// Issue records a problem at line (0 for the whole output).
func (s *Scanner) Issue(code string, line int, fields ...evidence.Field) {
	i := evidence.Issue{Code: code, Fields: fields}
	if s.source != nil {
		ref := s.source.At(line)
		i.Source = &ref
	}
	s.issues = append(s.issues, i)
}

// Fail records an issue that invalidates the whole output.
func (s *Scanner) Fail(code string) {
	s.failed = true
	s.Issue(code, 0)
}

// Unparsed records every unmarked line of the cursor. At most limit lines
// are recorded individually (0 means 20); the rest are summarized in one
// issue carrying the remaining count.
func (s *Scanner) Unparsed(c *Cursor, limit int) {
	if limit <= 0 {
		limit = 20
	}
	n := 0
	for l := range c.Unparsed() {
		if n == limit {
			i := evidence.Issue{Code: IssueUnparsed, Count: c.UnparsedCount() - limit}
			if s.source != nil {
				ref := *s.source
				i.Source = &ref
			}
			s.issues = append(s.issues, i)
			return
		}
		s.Issue(IssueUnparsed, l.Number)
		n++
	}
}

// Issues returns a copy of the recorded issues.
func (s *Scanner) Issues() []evidence.Issue { return append([]evidence.Issue{}, s.issues...) }

// Status returns failed after Fail, complete when the response was complete
// and nothing was recorded, and partial otherwise.
func (s *Scanner) Status(responseComplete bool) evidence.Status {
	switch {
	case s.failed:
		return evidence.StatusFailed
	case responseComplete && len(s.issues) == 0:
		return evidence.StatusComplete
	default:
		return evidence.StatusPartial
	}
}
