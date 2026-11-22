package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gatework/netops/event"
	"github.com/gatework/netops/secret"
	"github.com/gatework/netops/store/disk"
	"github.com/gatework/netops/term"
)

// ErrInvalid reports an invalid sink configuration.
var ErrInvalid = errors.New("notify: invalid")

// File appends events as JSON Lines to a private file. It is safe for
// concurrent use within a process.
type File struct {
	path string
	mu   sync.Mutex // serializes appends
}

var _ event.Sink = (*File)(nil)

// NewFile returns a file sink; the directory is created privately.
func NewFile(path string) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalid)
	}
	if err := disk.PrepareDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("notify: %w", err)
	}
	return &File{path: path}, nil
}

// Deliver appends one line per event and syncs the file.
func (f *File) Deliver(_ context.Context, events []event.Event) error {
	var b bytes.Buffer
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("notify: encode event: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := disk.OpenAppend(f.path)
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	if _, err := out.Write(b.Bytes()); err != nil {
		return errors.Join(fmt.Errorf("notify: append: %w", err), out.Close())
	}
	if err := errors.Join(out.Sync(), out.Close()); err != nil {
		return fmt.Errorf("notify: sync: %w", err)
	}
	return nil
}

// Writer prints one sanitized line per event, for terminals and logs.
type Writer struct {
	W io.Writer
}

// Deliver writes "time severity kind device summary".
func (w Writer) Deliver(_ context.Context, events []event.Event) error {
	for _, e := range events {
		line := fmt.Sprintf("%s %-8s %-22s %-16s %s\n", e.At.Format(time.DateTime), e.Severity, e.Kind,
			term.Dash(e.Device), term.Sanitize(e.Summary))
		if _, err := io.WriteString(w.W, line); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
	}
	return nil
}

// WebhookOptions configure a webhook.
type WebhookOptions struct {
	URL string
	// Secret signs bodies; empty sends unsigned bodies.
	Secret secret.Secret
	// Headers are added to every request, such as an authorization token.
	Headers map[string]secret.Secret
	// Timeout bounds each attempt; zero means 10s.
	Timeout time.Duration
	// Retries after the first attempt; zero means 2, negative disables.
	Retries int
	// Backoff before the first retry, doubled each time; zero means 1s.
	Backoff time.Duration
	// AllowHTTP permits a plain-HTTP URL, for local relays only.
	AllowHTTP bool
	// Client is the HTTP client; nil uses a dedicated client.
	Client *http.Client
}

// Webhook posts events as a JSON document {"events":[...]}.
type Webhook struct {
	o WebhookOptions
	u *url.URL
}

var _ event.Sink = (*Webhook)(nil)

// NewWebhook validates the options.
func NewWebhook(o WebhookOptions) (*Webhook, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && o.AllowHTTP)) {
		return nil, fmt.Errorf("%w: webhook URL must be https without credentials", ErrInvalid)
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	switch {
	case o.Retries == 0:
		o.Retries = 2
	case o.Retries < 0:
		o.Retries = 0
	}
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{}
	}
	return &Webhook{o: o, u: u}, nil
}

// Sign returns the signature header value of body under key.
func Sign(key secret.Secret, body []byte) string {
	m := hmac.New(sha256.New, []byte(key.Reveal()))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// StatusError reports a non-2xx answer.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string { return "notify: webhook answered " + strconv.Itoa(e.Code) }

// Deliver posts the events, retrying transient failures.
func (w *Webhook) Deliver(ctx context.Context, events []event.Event) error {
	body, err := json.Marshal(struct {
		Events []event.Event `json:"events"`
	}{events})
	if err != nil {
		return fmt.Errorf("notify: encode: %w", err)
	}
	backoff := w.o.Backoff
	var last error
	for attempt := 0; attempt <= w.o.Retries; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return errors.Join(last, ctx.Err())
			case <-t.C:
			}
			backoff *= 2
		}
		retry, err := w.post(ctx, body, len(events))
		if err == nil {
			return nil
		}
		last = err
		if !retry {
			break
		}
	}
	return last
}

// post sends one attempt and reports whether a failure is worth retrying.
func (w *Webhook) post(ctx context.Context, body []byte, n int) (retry bool, err error) {
	actx, cancel := context.WithTimeout(ctx, w.o.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, w.u.String(), bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("notify: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "netops-notify")
	req.Header.Set("X-Netops-Event-Count", strconv.Itoa(n))
	if !w.o.Secret.Empty() {
		req.Header.Set("X-Netops-Signature", Sign(w.o.Secret, body))
	}
	for k, v := range w.o.Headers {
		req.Header.Set(k, v.Reveal())
	}
	resp, err := w.o.Client.Do(req)
	if err != nil {
		// The URL may carry a token in its path; report only the class.
		return ctx.Err() == nil, fmt.Errorf("notify: webhook unreachable: %s", networkClass(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return true, &StatusError{Code: resp.StatusCode}
	}
	return false, &StatusError{Code: resp.StatusCode}
}

func networkClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	}
	return "network error"
}
