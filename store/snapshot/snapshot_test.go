package snapshot_test

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/store/snapshot"
)

var (
	host = netip.MustParseAddr("192.0.2.1")
	t0   = time.Date(2026, 11, 13, 7, 47, 25, 0, time.UTC)
)

func TestStore_roundTripAndMerge(t *testing.T) {
	t.Parallel()
	st, err := snapshot.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := capture.Capture{Host: host, Prompt: "<AC1>", Outputs: []capture.Output{
		{Command: "display clients", Raw: "display clients\r\n3 clients\r\n<AC1>", RecordedAt: t0, Duration: 12 * time.Millisecond},
		{Command: "display rf", Failure: failure.CodeRejected, RecordedAt: t0},
	}}
	if err := st.Save(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Load(t.Context(), host)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if o, _ := got.Answer("display clients"); o.Raw != c.Outputs[0].Raw || !o.RecordedAt.Equal(t0) || o.Duration != 12*time.Millisecond {
		t.Errorf("output %+v", o)
	}
	if o, _ := got.Output("display rf"); o.Failure != failure.CodeRejected {
		t.Errorf("failure %+v", o)
	}
	merged, err := st.Merge(t.Context(), capture.Capture{Host: host, Outputs: []capture.Output{{Command: "display rf", Raw: "ok", RecordedAt: t0.Add(time.Hour)}}})
	if err != nil || len(merged.Outputs) != 2 {
		t.Fatalf("merge %v %v", merged, err)
	}
	if err := st.SetStatus(t.Context(), host, cache.Status{LastAttempt: t0, Failure: failure.CodeTimeout}); err != nil {
		t.Fatal(err)
	}
	if s, ok, _ := st.Status(t.Context(), host); !ok || s.Failure != failure.CodeTimeout {
		t.Errorf("status %+v", s)
	}
	hosts, _ := st.Hosts()
	if len(hosts) != 1 || hosts[0] != host {
		t.Errorf("hosts %v (status and lock directories must not count)", hosts)
	}
}

// TestReadDir_netConnectorLayout reads a synthetic directory in the layout
// written by net-connector batches, which has no schema field.
func TestReadDir_netConnectorLayout(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "192.0.2.7")
	_ = os.Mkdir(dir, 0o700)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("01-screen-length-disable.output", "screen-length disable\r\r\n<AC7>")
	write("01-screen-length-disable.json", `{"command":"screen-length disable","success":true,"duration_ms":188,"bytes":`+fmt.Sprint(len("screen-length disable\r\r\n<AC7>"))+`,`+
		`"sha256":"`+sha("screen-length disable\r\r\n<AC7>")+`","prompt_before":"\u0000<AC7>","prompt_after":"<AC7>","recorded_at":"2026-11-13T07:47:25Z"}`)
	write("02-display-x.json", `{"command":"display x","success":false,"bytes":0,"sha256":"","error":"deadline_exceeded","recorded_at":"2026-11-13T07:47:26Z"}`)
	c, err := snapshot.ReadDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Prompt != "<AC7>" || len(c.Outputs) != 2 {
		t.Fatalf("capture %+v", c)
	}
	if o, _ := c.Output("display x"); o.Failure != failure.CodeTimeout {
		t.Errorf("legacy code %q", o.Failure)
	}
	write("01-screen-length-disable.output", "tampered")
	if _, err := snapshot.ReadDir(t.Context(), dir); !errors.Is(err, snapshot.ErrCorrupt) {
		t.Errorf("tampering: %v", err)
	}
}

func TestStore_ImportAndPrune(t *testing.T) {
	t.Parallel()
	src, _ := snapshot.Open(t.TempDir())
	_ = src.Save(t.Context(), capture.Capture{Host: host, Outputs: []capture.Output{{Command: "a", Raw: "x", RecordedAt: t0}}})
	dst, _ := snapshot.Open(t.TempDir())
	r, err := dst.Import(t.Context(), src.Root())
	if err != nil || r.Devices != 1 || r.Updated != 1 {
		t.Fatalf("import %+v %v", r, err)
	}
	if r, _ := dst.Import(t.Context(), src.Root()); r.Updated != 0 {
		t.Error("re-import of older data updated the store")
	}
	_ = os.Mkdir(filepath.Join(dst.Root(), ".192.0.2.1.123"), 0o700)
	if n, _ := dst.Prune(time.Now().Add(time.Hour)); n != 1 {
		t.Errorf("pruned %d", n)
	}
}
