package parse_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/parse"
)

func texts(lines []parse.Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestNew_lineEndings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\r\nb\rc\n\nd", []string{"a", "b", "c", "", "d"}},
	}
	for _, tc := range tests {
		c, err := parse.New(t.Context(), tc.in, parse.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if got := texts(c.Lines()); !slices.Equal(got, tc.want) {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
		if c.Digest() != evidence.DigestString(tc.in) {
			t.Errorf("%q digest mismatch", tc.in)
		}
	}
}

func TestNew_limits(t *testing.T) {
	t.Parallel()
	_, err := parse.New(t.Context(), "a\nb\nc", parse.Limits{MaxLines: 2})
	var le *parse.LimitError
	if !errors.As(err, &le) || le.Resource != "lines" || !errors.Is(err, parse.ErrLimit) {
		t.Errorf("line limit: %v", err)
	}
	if _, err := parse.New(t.Context(), "abc", parse.Limits{MaxBytes: 2}); !errors.Is(err, parse.ErrLimit) {
		t.Errorf("byte limit: %v", err)
	}
	if _, err := parse.New(t.Context(), "", parse.Limits{MaxBytes: -1}); !errors.Is(err, parse.ErrInvalidLimits) {
		t.Errorf("negative limit: %v", err)
	}
	var nilContent *parse.Content
	if nilContent.Len() != 0 || nilContent.Text() != "" {
		t.Error("nil content is not empty")
	}
}

func TestCursor_unparsedBookkeeping(t *testing.T) {
	t.Parallel()
	c, _ := parse.New(t.Context(), strings.Repeat("x\n", 130), parse.Limits{})
	cur := c.Cursor()
	if err := cur.Mark(1, 129); err != nil {
		t.Fatal(err)
	}
	if cur.UnparsedCount() != 1 {
		t.Fatalf("unparsed = %d", cur.UnparsedCount())
	}
	l, ok := cur.NextUnparsed()
	if !ok || l.Number != 130 {
		t.Errorf("NextUnparsed = %d %v", l.Number, ok)
	}
	_ = cur.Unmark(64, 65)
	var got []int
	for l := range cur.Unparsed() {
		got = append(got, l.Number)
	}
	if !slices.Equal(got, []int{64, 65, 130}) {
		t.Errorf("Unparsed = %v", got)
	}
	cp := cur.Checkpoint()
	if c.Cursor().Restore(cp) == nil {
		t.Error("foreign checkpoint accepted")
	}
}

func TestScanner(t *testing.T) {
	t.Parallel()
	c, _ := parse.New(t.Context(), "Name : ap1\ngarbage\n", parse.Limits{})
	cur := c.Cursor()
	_ = cur.Mark(1, 1)
	s := parse.NewScanner(evidence.SourceRef{Command: "display ap"})
	s.Unparsed(cur, 0)
	is := s.Issues()
	if len(is) != 1 || is[0].Source.Line != 2 || s.Status(true) != evidence.StatusPartial {
		t.Errorf("issues %+v", is)
	}
}

func TestColumns(t *testing.T) {
	t.Parallel()
	cols, ok := parse.HeaderColumns("Port      Name      Status", "Port", "Name", "Status")
	if !ok || !slices.Equal(cols.Cells("Eth1/1    uplink    connected"), []string{"Eth1/1", "uplink", "connected"}) {
		t.Error("header columns")
	}
	r, ok := parse.RulerColumns("----- ---- --", '-')
	if !ok || !slices.Equal(r.Cells("ab    cd   e"), []string{"ab", "cd", "e"}) {
		t.Errorf("ruler columns %v", r.Cells("ab    cd   e"))
	}
	if _, ok := parse.RulerColumns("--x--", '-'); ok {
		t.Error("ruler with text accepted")
	}
}

func TestTree(t *testing.T) {
	t.Parallel()
	c, _ := parse.New(t.Context(), "#\n sysname AC\n#\ninterface V1\n ip address 1\n#\n ssh enable\n", parse.Limits{})
	root := parse.Tree(c.All(), parse.TreeOptions{Reset: func(l parse.Line) bool { return strings.TrimSpace(l.Text) == "#" }})
	if got := texts2(root.Children); !slices.Equal(got, []string{"sysname AC", "interface V1", "ssh enable"}) {
		t.Errorf("top level = %v", got)
	}
	n, ok := root.Find("interface V1", "ip address 1")
	if !ok || !slices.Equal(n.Path(), []string{"interface V1", "ip address 1"}) || n.Line.Number != 5 {
		t.Errorf("Find = %+v", n)
	}
}

func texts2(ns []*parse.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Text
	}
	return out
}

func TestLabels_duplicateIsDeterministic(t *testing.T) {
	t.Parallel()
	for range 20 {
		_, err := parse.Labels{"b": {"AP Name"}, "a": {"ap  name"}}.Compile()
		var dup *parse.DuplicateLabelError
		if !errors.As(err, &dup) || dup.Fields != [2]string{"a", "b"} {
			t.Fatalf("duplicate = %v", err)
		}
	}
	tab := parse.Labels{"name": {"AP Name", "Name"}}.MustCompile()
	if f, ok := tab.Field("ap   NAME"); !ok || f != "name" {
		t.Error("normalized lookup failed")
	}
}

func FuzzNew(f *testing.F) {
	for _, s := range []string{"", "a", "a\r\n", "\r\r\n\n", "x\ry"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := parse.New(t.Context(), s, parse.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for l := range c.All() {
			b.WriteString(l.Raw)
			if strings.ContainsAny(l.Text, "\r\n") {
				t.Fatalf("line %d keeps a terminator", l.Number)
			}
		}
		if b.String() != s {
			t.Fatal("lines do not reassemble the source")
		}
	})
}

func ExampleContent_Cursor() {
	c, _ := parse.New(context.Background(), "Name : ap1\nState : Run\n???\n", parse.Limits{})
	cur := c.Cursor()
	for l, ok := cur.Next(); ok; l, ok = cur.Next() {
		if label, value, ok := parse.SplitLabel(l.Text, ":"); ok {
			fmt.Println(label, "=", value)
			_ = cur.Mark(l.Number, l.Number)
		}
	}
	fmt.Println("unparsed:", cur.UnparsedCount())
	// Output:
	// Name = ap1
	// State = Run
	// unparsed: 1
}

func TestLines_matchesContent(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "a", "a\r\nb\rc\n\nd", "x\n"} {
		c, _ := parse.New(t.Context(), s, parse.Limits{})
		if got, want := slices.Collect(parse.Lines(s)), c.Lines(); !slices.Equal(got, want) {
			t.Errorf("%q: %v != %v", s, got, want)
		}
	}
}
