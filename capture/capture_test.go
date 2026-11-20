package capture_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/failure"
)

func TestOutput_Body(t *testing.T) {
	t.Parallel()
	o := capture.Output{Command: "display version", Raw: "display version\r\r\nH3C Comware 7.1\r\nUptime 1d\r\n\r\n<AC1>", Prompt: "\x00<AC1>"}
	lines, err := o.Body(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, fmt.Sprint(l.Number, ":", l.Text))
	}
	if !slices.Equal(got, []string{"2:H3C Comware 7.1", "3:Uptime 1d"}) {
		t.Errorf("Body = %v", got)
	}
	if !capture.PromptLike()("[AC1]") || !capture.PromptLike()("sw1(config-if)#") || capture.PromptLike()("total 5 items") {
		t.Error("PromptLike")
	}
}

func TestCapture_Merge(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	host := netip.MustParseAddr("192.0.2.1")
	old := capture.Capture{Host: host, Prompt: "<A>", Outputs: []capture.Output{
		{Command: "a", Raw: "old-a", RecordedAt: t0},
		{Command: "b", Raw: "old-b", RecordedAt: t0},
		{Command: "c", Failure: failure.CodeTimeout, RecordedAt: t0},
	}}
	fresh := capture.Capture{Host: host, Outputs: []capture.Output{
		{Command: "a", Raw: "new-a", RecordedAt: t0.Add(time.Hour)},
		{Command: "b", Failure: failure.CodeRejected, RecordedAt: t0.Add(time.Hour)},
		{Command: "c", Failure: failure.CodeRejected, RecordedAt: t0.Add(time.Hour)},
	}}
	m := old.Merge(fresh)
	want := map[string]string{"a": "new-a", "b": "old-b"}
	for cmd, raw := range want {
		if o, ok := m.Answer(cmd); !ok || o.Raw != raw {
			t.Errorf("%s = %+v", cmd, o)
		}
	}
	if o, _ := m.Output("c"); o.Failure != failure.CodeRejected {
		t.Errorf("latest failure not kept: %v", o.Failure)
	}
	if m.Prompt != "<A>" || len(m.Outputs) != 3 || len(old.Outputs) != 3 {
		t.Error("merge changed its input or lost the prompt")
	}
}

func ExampleCapture_Merge() {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cached := capture.Capture{Outputs: []capture.Output{{Command: "show clients", Raw: "3 clients", RecordedAt: t0}}}
	failed := capture.Capture{Outputs: []capture.Output{{Command: "show clients", Failure: failure.CodeTimeout, RecordedAt: t0.Add(time.Minute)}}}
	o, _ := cached.Merge(failed).Answer("show clients")
	fmt.Println(o.Raw)
	// Output: 3 clients
}
