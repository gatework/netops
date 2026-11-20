package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/store/disk"

	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/store"
)

// Schema is the sidecar schema written by this package.
const Schema = 1

// MaxOutput bounds one stored output.
const MaxOutput = 64 << 20

const (
	maxSidecar = 1 << 20
	statusDir  = ".status"
	lockWait   = 10 * time.Second
	staleAge   = 10 * time.Minute
)

// ErrCorrupt reports a sidecar or output that fails verification.
var ErrCorrupt = errors.New("snapshot: corrupt capture")

// Sidecar describes one stored output. Field names follow the
// net-connector batch layout.
type Sidecar struct {
	Schema       int       `json:"schema,omitempty"`
	Command      string    `json:"command"`
	Success      bool      `json:"success"`
	DurationMS   int64     `json:"duration_ms"`
	Bytes        int       `json:"bytes"`
	SHA256       string    `json:"sha256"`
	PromptBefore string    `json:"prompt_before,omitempty"`
	PromptAfter  string    `json:"prompt_after,omitempty"`
	RecordedAt   time.Time `json:"recorded_at"`
	Error        string    `json:"error,omitempty"`
}

// Store is a snapshot root. It is safe for concurrent use.
type Store struct {
	root *store.Store
}

// Open opens or creates a private snapshot root.
func Open(root string) (*Store, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: st}, nil
}

// Root returns the absolute root directory.
func (s *Store) Root() string { return s.root.Root() }

// Hosts returns the stored device addresses in address order.
func (s *Store) Hosts() ([]netip.Addr, error) { return hosts(s.root.Root()) }

// List returns the device directories under root in address order. It
// only reads, so root need not be a private store (a corpus, an export).
func List(root string) ([]string, error) {
	hs, err := hosts(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = filepath.Join(root, h.String())
	}
	return out, nil
}

func hosts(root string) ([]netip.Addr, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	var out []netip.Addr
	for _, e := range entries {
		if a, err := netip.ParseAddr(e.Name()); err == nil && e.IsDir() && a.String() == e.Name() {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return out, nil
}

// Load returns the capture of host; ok is false when none is stored.
func (s *Store) Load(ctx context.Context, host netip.Addr) (c capture.Capture, ok bool, err error) {
	dir := s.dir(host)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return capture.Capture{}, false, nil
	}
	c, err = ReadDir(ctx, dir)
	if err != nil {
		return capture.Capture{}, false, err
	}
	return c, true, nil
}

// LoadAll returns every stored capture in address order.
func (s *Store) LoadAll(ctx context.Context) ([]capture.Capture, error) {
	hs, err := s.Hosts()
	if err != nil {
		return nil, err
	}
	out := make([]capture.Capture, 0, len(hs))
	for _, h := range hs {
		c, _, err := s.Load(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Save atomically replaces the stored capture of c.Host. Callers that merge
// should use Merge, which holds the device lock.
func (s *Store) Save(ctx context.Context, c capture.Capture) error {
	if !c.Host.IsValid() {
		return fmt.Errorf("snapshot: capture without host")
	}
	return WriteDir(ctx, s.dir(c.Host), c)
}

// Merge combines fresh with the stored capture of the same device under the
// device lock and saves the result, which it returns.
func (s *Store) Merge(ctx context.Context, fresh capture.Capture) (capture.Capture, error) {
	lock, err := s.Lock(ctx, fresh.Host)
	if err != nil {
		return capture.Capture{}, err
	}
	defer lock.Close()
	merged := fresh
	if old, ok, err := s.Load(ctx, fresh.Host); err != nil {
		return capture.Capture{}, err
	} else if ok {
		merged = old.Merge(fresh)
	}
	if err := s.Save(ctx, merged); err != nil {
		return capture.Capture{}, err
	}
	return merged, nil
}

// Lock acquires the writer lock of host.
func (s *Store) Lock(ctx context.Context, host netip.Addr) (io.Closer, error) {
	return s.root.Lock(ctx, "snapshot/"+host.String(), lockWait)
}

// Status returns the refresh status of host; ok is false when none is
// recorded or the record is unreadable (a damaged status only loses
// back-off, so it is not an error).
func (s *Store) Status(ctx context.Context, host netip.Addr) (st cache.Status, ok bool, err error) {
	var out cache.Status
	err = s.root.ReadJSON(ctx, statusDir+"/"+host.String()+".json", 64<<10, &out)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return cache.Status{}, false, nil
	case err != nil:
		return cache.Status{}, false, nil //nolint:nilerr // see doc: a damaged status is treated as absent
	}
	return out, true, nil
}

// SetStatus records the refresh status of host.
func (s *Store) SetStatus(ctx context.Context, host netip.Addr, st cache.Status) error {
	_, err := s.root.WriteJSON(ctx, statusDir+"/"+host.String()+".json", st)
	return err
}

// Prune removes staging and backup directories older than ten minutes,
// left behind by interrupted saves. It returns how many it removed.
func (s *Store) Prune(now time.Time) (int, error) {
	entries, err := os.ReadDir(s.root.Root())
	if err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !leftover(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < staleAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root.Root(), e.Name())); err != nil {
			return n, fmt.Errorf("snapshot: prune: %w", err)
		}
		n++
	}
	return n, nil
}

// ImportResult counts an import.
type ImportResult struct {
	Devices int `json:"devices"`
	Updated int `json:"updated"`
}

// Import merges every capture found under src (read-only) into the store
// when it holds a newer output for at least one command.
func (s *Store) Import(ctx context.Context, src string) (ImportResult, error) {
	var r ImportResult
	abs, err := filepath.Abs(src)
	if err != nil {
		return r, fmt.Errorf("snapshot: %w", err)
	}
	if abs == s.Root() {
		return r, nil
	}
	hs, err := hosts(abs)
	if err != nil {
		return r, err
	}
	for _, h := range hs {
		fresh, err := ReadDir(ctx, filepath.Join(abs, h.String()))
		if err != nil {
			return r, err
		}
		r.Devices++
		old, ok, err := s.Load(ctx, h)
		if err != nil {
			return r, err
		}
		if ok && !newer(old, fresh) {
			continue
		}
		if _, err := s.Merge(ctx, fresh); err != nil {
			return r, err
		}
		r.Updated++
	}
	return r, nil
}

func newer(old, fresh capture.Capture) bool {
	for _, o := range fresh.Outputs {
		prev, ok := old.Answer(o.Command)
		if o.OK() && (!ok || o.RecordedAt.After(prev.RecordedAt)) {
			return true
		}
	}
	return false
}

func (s *Store) dir(host netip.Addr) string {
	return filepath.Join(s.root.Root(), host.Unmap().String())
}

// ReadDir reads one device directory in any supported layout. The host is
// taken from the directory name.
func ReadDir(ctx context.Context, dir string) (capture.Capture, error) {
	return ReadDirSome(ctx, dir, nil)
}

// ReadDirSome is ReadDir limited to the commands keep accepts; nil keeps
// every command. Skipped outputs are neither read nor verified, so loading
// one dimension of a large fleet stays cheap.
func ReadDirSome(ctx context.Context, dir string, keep func(command string) bool) (capture.Capture, error) {
	host, err := netip.ParseAddr(filepath.Base(dir))
	if err != nil {
		return capture.Capture{}, fmt.Errorf("snapshot: %s is not a device directory", filepath.Base(dir))
	}
	sidecars, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return capture.Capture{}, fmt.Errorf("snapshot: %w", err)
	}
	slices.Sort(sidecars)
	c := capture.Capture{Host: host, Outputs: []capture.Output{}}
	for _, path := range sidecars {
		if err := ctx.Err(); err != nil {
			return capture.Capture{}, err
		}
		o, meta, err := readOutput(ctx, path, keep)
		if errors.Is(err, errSkipped) {
			continue
		}
		if err != nil {
			return capture.Capture{}, err
		}
		if p := cleanPrompt(meta.PromptAfter); p != "" {
			c.Prompt = p
		}
		c.Outputs = append(c.Outputs, o)
	}
	return c, nil
}

var errSkipped = errors.New("snapshot: skipped")

func readOutput(ctx context.Context, path string, keep func(string) bool) (capture.Output, Sidecar, error) {
	var meta Sidecar
	raw, _, err := disk.ReadFile(ctx, path, maxSidecar)
	if err != nil {
		return capture.Output{}, meta, fmt.Errorf("snapshot: %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Command == "" || meta.Schema > Schema {
		return capture.Output{}, meta, fmt.Errorf("%w: %s", ErrCorrupt, filepath.Base(path))
	}
	if keep != nil && !keep(meta.Command) {
		return capture.Output{}, meta, errSkipped
	}
	outPath := strings.TrimSuffix(path, ".json") + ".output"
	body, sha, err := disk.ReadFile(ctx, outPath, MaxOutput)
	switch {
	case errors.Is(err, fs.ErrNotExist) && meta.Bytes == 0:
		body = nil
	case err != nil:
		return capture.Output{}, meta, fmt.Errorf("snapshot: %s: %w", filepath.Base(outPath), err)
	case len(body) != meta.Bytes || meta.SHA256 != "" && sha != meta.SHA256:
		return capture.Output{}, meta, fmt.Errorf("%w: %s", ErrCorrupt, filepath.Base(outPath))
	}
	o := capture.Output{
		Command: meta.Command, Raw: string(body), Prompt: cleanPrompt(meta.PromptAfter),
		RecordedAt: meta.RecordedAt, Duration: time.Duration(meta.DurationMS) * time.Millisecond,
	}
	if !meta.Success {
		o.Failure = legacyCode(meta.Error, len(body) > 0)
	}
	return o, meta, nil
}

// legacyCode maps a stored error to a failure code; earlier layouts used
// connector session codes.
func legacyCode(code string, partial bool) failure.Code {
	var c failure.Code
	if err := c.UnmarshalText([]byte(code)); err == nil && c != failure.CodeUnset {
		return c
	}
	switch code {
	case "deadline_exceeded":
		return failure.CodeTimeout
	case "command_failed":
		return failure.CodeRejected
	case "session_scope_ended":
		return failure.CodeClosed
	case "":
		if partial {
			return failure.CodeIncomplete
		}
		return failure.CodeRejected
	}
	return failure.CodeInternal
}

func cleanPrompt(p string) string { return strings.TrimSpace(strings.ReplaceAll(p, "\x00", "")) }

// WriteDir atomically replaces dir with the capture.
func WriteDir(ctx context.Context, dir string, c capture.Capture) (err error) {
	if err := disk.PrepareDir(filepath.Dir(dir)); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".")
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	for i, o := range c.Outputs {
		if err := writeOutput(ctx, staging, i+1, c.Prompt, o); err != nil {
			return err
		}
	}
	if err := disk.SyncDir(staging); err != nil {
		return fmt.Errorf("snapshot: sync: %w", err)
	}
	return replaceDir(staging, dir)
}

func writeOutput(ctx context.Context, dir string, n int, prompt string, o capture.Output) error {
	if len(o.Raw) > MaxOutput {
		return fmt.Errorf("snapshot: output of %q exceeds %d bytes", o.Command, MaxOutput)
	}
	base := filepath.Join(dir, fmt.Sprintf("%02d-%s", n, fileName(o.Command)))
	p := o.Prompt
	if p == "" {
		p = prompt
	}
	meta := Sidecar{
		Schema: Schema, Command: o.Command, Success: o.OK(), DurationMS: o.Duration.Milliseconds(),
		Bytes: len(o.Raw), SHA256: string(evidence.DigestString(o.Raw)), PromptBefore: p, PromptAfter: p,
		RecordedAt: o.RecordedAt.UTC(), Error: string(o.Failure),
	}
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if _, err := disk.WriteFile(ctx, base+".output", []byte(o.Raw)); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if _, err := disk.WriteFile(ctx, base+".json", body); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	return nil
}

// replaceDir swaps staging into dir, keeping the old directory as
// "<dir>.old" until the swap succeeds.
func replaceDir(staging, dir string) error {
	old := ""
	if _, err := os.Stat(dir); err == nil {
		old = dir + ".old"
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		if err := os.Rename(dir, old); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
	}
	if err := os.Rename(staging, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir)
		}
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := disk.SyncDir(filepath.Dir(dir)); err != nil {
		return fmt.Errorf("snapshot: sync: %w", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// fileName keeps the layout's readable names: runs of spaces become one
// "-", other unsafe bytes become "-". The NN- prefix keeps names unique.
func fileName(command string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, strings.Join(strings.Fields(command), " "))
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func leftover(name string) bool {
	if ip, ok := strings.CutSuffix(name, ".old"); ok {
		_, err := netip.ParseAddr(ip)
		return err == nil
	}
	rest, ok := strings.CutPrefix(name, ".")
	if !ok {
		return false
	}
	i := strings.LastIndex(rest, ".")
	if i <= 0 {
		return false
	}
	_, err := netip.ParseAddr(rest[:i])
	return err == nil
}
