package archive_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/store/archive"
)

var day = time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)

func backup(text string, at time.Time) config.Backup {
	return config.Backup{Device: "sw1", Text: text, Normalized: evidence.DigestString(text), At: at, Source: "backup"}
}

func TestArchive(t *testing.T) {
	t.Parallel()
	a, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	r1, err := a.Put(ctx, backup("hostname sw1\n", day))
	if err != nil || !r1.Changed || r1.Version.Seq != 1 {
		t.Fatalf("first put %+v %v", r1, err)
	}
	r2, _ := a.Put(ctx, backup("hostname sw1\n", day.Add(24*time.Hour)))
	if r2.Changed {
		t.Error("unchanged backup stored again")
	}
	if checked, _ := a.Checked(ctx, "sw1"); !checked.Equal(day.Add(24 * time.Hour)) {
		t.Errorf("checked %v", checked)
	}
	r3, _ := a.Put(ctx, backup("hostname sw1-new\n", day.Add(48*time.Hour)))
	if !r3.Changed || r3.Previous.Seq != 1 || r3.Version.Seq != 2 {
		t.Errorf("change %+v", r3)
	}
	if _, err := a.Put(ctx, backup("x", day)); err == nil {
		t.Error("backup older than the latest version accepted")
	}
	v, err := a.At(ctx, "sw1", day.Add(30*time.Hour))
	if err != nil || v.Seq != 1 {
		t.Errorf("At = %+v %v", v, err)
	}
	if text, _ := a.Get(ctx, v); text != "hostname sw1\n" {
		t.Errorf("Get = %q", text)
	}
	if _, err := a.At(ctx, "sw1", day.Add(-time.Hour)); !errors.Is(err, archive.ErrNotFound) {
		t.Errorf("before first: %v", err)
	}
	n, err := a.Prune(ctx, archive.Retention{Keep: 1}, day.Add(72*time.Hour))
	if err != nil || n != 1 {
		t.Errorf("prune %d %v", n, err)
	}
	if _, err := a.Get(ctx, v); !errors.Is(err, archive.ErrNotFound) {
		t.Error("unreferenced object not swept")
	}
	ids, _ := a.Devices(ctx)
	if len(ids) != 1 || ids[0] != "sw1" {
		t.Errorf("devices %v", ids)
	}
}
