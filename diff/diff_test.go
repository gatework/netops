package diff_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/parse"
)

// apply rebuilds both sides from a script.
func apply(s diff.Script) (a, b []string) {
	for _, e := range s.Edits {
		switch e.Op {
		case diff.OpEqual:
			a, b = append(a, e.Text), append(b, e.Text)
		case diff.OpDelete:
			a = append(a, e.Text)
		case diff.OpInsert:
			b = append(b, e.Text)
		}
	}
	return a, b
}

func TestLines_reconstructs(t *testing.T) {
	t.Parallel()
	a := strings.Fields("a b c d e f a b")
	b := strings.Fields("a x c d f a b b")
	s := diff.Lines(a, b, diff.Options{})
	ga, gb := apply(s)
	if !slices.Equal(ga, a) || !slices.Equal(gb, b) || !s.Exact {
		t.Errorf("script does not reconstruct: %v %v", ga, gb)
	}
	ins, del := s.Stats()
	if ins != 2 || del != 2 {
		t.Errorf("stats %d/%d, want minimal 2/2", ins, del)
	}
}

func TestLines_budget(t *testing.T) {
	t.Parallel()
	a := slices.Repeat([]string{"x", "y"}, 50)
	b := slices.Repeat([]string{"y", "x"}, 50)
	s := diff.Lines(a, b, diff.Options{Budget: 10})
	ga, gb := apply(s)
	if s.Exact || !slices.Equal(ga, a) || !slices.Equal(gb, b) {
		t.Error("over-budget gap is not a correct inexact replacement")
	}
}

func TestUnified_golden(t *testing.T) {
	t.Parallel()
	a := strings.Split("hostname sw1\ninterface Eth1/1\n description old\n shutdown\nntp server 192.0.2.1", "\n")
	b := strings.Split("hostname sw1\ninterface Eth1/1\n description new\n shutdown\nntp server 192.0.2.1\nntp server 192.0.2.2", "\n")
	got := diff.Unified(diff.Lines(a, b, diff.Options{}), "a/sw1", "b/sw1", 1, nil)
	golden := filepath.Join("testdata", "unified.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(string(want), got); d != "" {
		t.Errorf("unified (-want +got):\n%s", d)
	}
}

func TestTree(t *testing.T) {
	t.Parallel()
	a := "interface Eth1/1\n description x\ninterface Eth1/2\n shutdown\nip access-list A\n 10 permit any\n 20 deny any\n"
	b := "interface Eth1/2\n shutdown\ninterface Eth1/1\n description y\nip access-list A\n 20 deny any\n 10 permit any\n"
	da, _ := config.Parse(t.Context(), a, config.Cisco(), parse.Limits{})
	db, _ := config.Parse(t.Context(), b, config.Cisco(), parse.Limits{})
	unordered := diff.Tree(da, db, diff.TreeOptions{})
	var got []string
	for _, c := range unordered {
		got = append(got, string(c.Kind)+" "+strings.Join(c.Path, ">")+" | "+c.Line)
	}
	want := []string{"removed interface Eth1/1 | description x", "added interface Eth1/1 | description y"}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("unordered (-want +got):\n%s", d)
	}
	ordered := diff.Tree(da, db, diff.TreeOptions{Ordered: func(p []string) bool { return len(p) == 1 && strings.HasPrefix(p[0], "ip access-list") }})
	if len(ordered) != 4 {
		t.Errorf("ACL reorder not reported: %+v", ordered)
	}
}

func TestMapsAndSets(t *testing.T) {
	t.Parallel()
	d := diff.Maps(map[string]int{"a": 1, "b": 2}, map[string]int{"b": 3, "c": 4}, func(x, y int) bool { return x == y })
	if len(d) != 3 || d[0].Kind != diff.KindRemoved || d[1].Kind != diff.KindChanged || d[2].Kind != diff.KindAdded {
		t.Errorf("Maps = %+v", d)
	}
	add, rem := diff.Sets([]string{"a", "b", "b"}, []string{"c", "b"})
	if !slices.Equal(add, []string{"c"}) || !slices.Equal(rem, []string{"a"}) {
		t.Errorf("Sets = %v %v", add, rem)
	}
}

func FuzzLines(f *testing.F) {
	f.Add("a b c", "a c d")
	f.Fuzz(func(t *testing.T, x, y string) {
		a, b := strings.Fields(x), strings.Fields(y)
		ga, gb := apply(diff.Lines(a, b, diff.Options{Budget: 64}))
		if !slices.Equal(ga, a) || !slices.Equal(gb, b) {
			t.Fatal("script does not reconstruct its inputs")
		}
	})
}

func ExampleUnified() {
	s := diff.Lines([]string{"ntp server 192.0.2.1"}, []string{"ntp server 192.0.2.2"}, diff.Options{})
	fmt.Print(diff.Unified(s, "before", "after", 0, nil))
	// Output:
	// --- before
	// +++ after
	// @@ -1 +1 @@
	// -ntp server 192.0.2.1
	// +ntp server 192.0.2.2
}
