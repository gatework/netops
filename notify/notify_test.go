package notify_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/notify"
	"github.com/gatework/netops/secret"
)

func events(t *testing.T) []event.Event {
	t.Helper()
	e, err := event.New(event.Event{Kind: event.KindChangeUncertain, Severity: assess.SeverityCritical,
		At: time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC), Device: "leaf1", Summary: "verify leaf1 by hand"})
	if err != nil {
		t.Fatal(err)
	}
	return []event.Event{e}
}

func TestWebhook(t *testing.T) {
	t.Parallel()
	key := secret.New("s3cret")
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Netops-Signature") != notify.Sign(key, body) || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var doc struct{ Events []event.Event }
		if json.Unmarshal(body, &doc) != nil || len(doc.Events) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	wh, err := notify.NewWebhook(notify.WebhookOptions{URL: srv.URL, Secret: key, Client: srv.Client(),
		Headers: map[string]secret.Secret{"Authorization": secret.New("Bearer tok")}, Backoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := wh.Deliver(t.Context(), events(t)); err != nil || calls.Load() != 2 {
		t.Errorf("delivery %v after %d calls", err, calls.Load())
	}
	bad, _ := notify.NewWebhook(notify.WebhookOptions{URL: srv.URL, Secret: secret.New("wrong"), Client: srv.Client(), Backoff: time.Millisecond})
	var se *notify.StatusError
	if err := bad.Deliver(t.Context(), events(t)); !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Errorf("4xx: %v", err)
	}
	if _, err := notify.NewWebhook(notify.WebhookOptions{URL: "http://hooks.example/x"}); !errors.Is(err, notify.ErrInvalid) {
		t.Error("plain http accepted")
	}
}

func TestFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "events", "events.jsonl")
	f, err := notify.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.Deliver(t.Context(), events(t)); err != nil {
			t.Fatal(err)
		}
	}
	fh, _ := os.Open(path)
	defer fh.Close()
	n := 0
	for sc := bufio.NewScanner(fh); sc.Scan(); n++ {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Device != "leaf1" {
			t.Fatalf("line %d: %v", n, err)
		}
	}
	if info, _ := os.Stat(path); n != 2 || info.Mode().Perm() != 0o600 {
		t.Errorf("%d lines, mode %v", n, info.Mode())
	}
	var b strings.Builder
	_ = notify.Writer{W: &b}.Deliver(t.Context(), events(t))
	if !strings.Contains(b.String(), "change.uncertain") {
		t.Error(b.String())
	}
}

func ExampleSign() {
	sig := notify.Sign(secret.New("k"), []byte(`{"events":[]}`))
	fmt.Println(strings.HasPrefix(sig, "sha256="), len(sig))
	// Output: true 71
}
