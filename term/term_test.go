package term_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gatework/netops/term"
)

func TestSanitize(t *testing.T) {
	t.Parallel()
	if got := term.Sanitize("ap1\x1b[2Jx\u202e"); got != `ap1\u001b[2Jx\u202e` {
		t.Errorf("Sanitize = %q", got)
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	p := term.Prompt{In: strings.NewReader("KICK 1\n"), Out: &out, Interactive: true}
	if err := p.Confirm("kick one client", "KICK 1", false); err != nil {
		t.Error(err)
	}
	p.In = strings.NewReader("yes\n")
	if err := p.Confirm("kick", "KICK 1", false); !errors.Is(err, term.ErrAborted) {
		t.Error("wrong answer accepted")
	}
	if err := (term.Prompt{}).Confirm("kick", "KICK 1", false); !errors.Is(err, term.ErrAborted) {
		t.Error("non-interactive confirm without --yes accepted")
	}
}

func ExampleTable() {
	t := term.NewTable("AP", "状态", "终端")
	t.Add("ap-01", "运行", "12")
	t.Add("ap-02", "", "0")
	_, _ = t.Render(os.Stdout, 0)
	// Output:
	// AP     状态  终端
	// ap-01  运行  12
	// ap-02  -     0
}
