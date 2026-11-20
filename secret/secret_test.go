package secret_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gatework/netops/secret"
)

func TestSecret_neverRenders(t *testing.T) {
	t.Parallel()
	s := secret.New("hunter2")
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("login", slog.Any("password", s))
	body, err := json.Marshal(struct{ P secret.Secret }{s})
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", s), "%#v": fmt.Sprintf("%#v", s), "String": s.String(),
		"json": string(body), "slog": logs.String(),
	} {
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s leaked the secret: %s", name, out)
		}
	}
	if s.Reveal() != "hunter2" || s.Empty() || !secret.New("").Empty() {
		t.Error("Reveal or Empty broken")
	}
}

func TestRedactor_Redact(t *testing.T) {
	t.Parallel()
	r := secret.NewRedactor(0, secret.New("abc"), secret.New("abcdef"), secret.New(""))
	if got := r.Redact("key abcdef and abc"); got != "key [REDACTED] and [REDACTED]" {
		t.Errorf("Redact = %q", got)
	}
	if !secret.NewRedactor(8, secret.New("short")).Empty() {
		t.Error("short secret not ignored")
	}
}

func ExampleRedactor() {
	r := secret.NewRedactor(0, secret.New("S3cret!"))
	fmt.Println(r.Redact("snmp-server community S3cret! RO"))
	// Output: snmp-server community [REDACTED] RO
}
