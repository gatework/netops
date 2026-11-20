package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"go.yaml.in/yaml/v3"
)

// MaxFileBytes bounds configuration and credentials files.
const MaxFileBytes = 1 << 20

// ReadFile reads a bounded regular file (at most MaxFileBytes). A missing
// file is reported with found=false when missingOK.
func ReadFile(ctx context.Context, path string, missingOK bool) (body []byte, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		if missingOK && errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%w: %s is not a regular file", ErrConfig, path)
	}
	body, err = io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > MaxFileBytes {
		return nil, false, fmt.Errorf("%w: %s exceeds %d bytes", ErrConfig, path, MaxFileBytes)
	}
	return body, true, nil
}

// ErrConfig reports an invalid configuration or environment file.
var ErrConfig = errors.New("bootstrap: invalid configuration")

// Env is the process context a tool starts from, injected for testing.
type Env struct {
	App     string            // lowercase tool name, such as "wireless"
	Environ map[string]string // the process environment
	Home    string            // the user's home directory
}

// Environ converts os.Environ-style pairs to a map.
func Environ(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, e := range pairs {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Discover returns the configuration path, or "" when none exists. An
// explicit $<APP>_CONFIG is returned even when missing, so that the read
// reports it.
func (e Env) Discover() string {
	if p := e.Environ[strings.ToUpper(e.App)+"_CONFIG"]; p != "" {
		return ResolvePath(p, ".", e.Home)
	}
	name := e.App + ".yml"
	candidates := []string{name}
	if xdg := e.Environ["XDG_CONFIG_HOME"]; xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, e.App, name))
	}
	if e.Home != "" {
		candidates = append(candidates, filepath.Join(e.Home, ".config", e.App, name))
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// ResolvePath expands "~" and "~/..." with home and makes a relative path
// relative to base.
func ResolvePath(p, base, home string) string {
	switch {
	case p == "":
		return ""
	case p == "~" && home != "":
		return home
	case strings.HasPrefix(p, "~/") && home != "":
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// DecodeYAML decodes one YAML document into v, rejecting unknown keys. An
// empty document leaves v unchanged.
func DecodeYAML(body []byte, v any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrConfig, err)
	}
	return nil
}

// ReadYAML reads a bounded file and decodes it strictly. A missing file is
// reported with found=false when missingOK.
func ReadYAML(ctx context.Context, path string, missingOK bool, v any) (found bool, err error) {
	body, found, err := ReadFile(ctx, path, missingOK)
	if err != nil {
		return false, fmt.Errorf("bootstrap: read %s: %w", path, err)
	}
	if !found {
		return false, nil
	}
	if err := DecodeYAML(body, v); err != nil {
		return true, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

// LoadEnv reads the credentials file and overlays the process environment.
// The file is explicit, or ".env" beside the configuration when it exists.
// It must not be accessible to group or others.
func LoadEnv(ctx context.Context, explicit, configPath string, process map[string]string) (map[string]string, error) {
	path := explicit
	if path == "" && configPath != "" {
		path = filepath.Join(filepath.Dir(configPath), ".env")
	}
	merged := map[string]string{}
	if path != "" {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist) && explicit == "":
		case err != nil:
			return nil, fmt.Errorf("bootstrap: credentials %s: %w", path, err)
		case info.Mode().Perm()&0o077 != 0:
			return nil, fmt.Errorf("%w: credentials %s must be mode 0600", ErrConfig, path)
		default:
			body, _, err := ReadFile(ctx, path, false)
			if err != nil {
				return nil, fmt.Errorf("bootstrap: credentials %s: %w", path, err)
			}
			if merged, err = godotenv.UnmarshalBytes(body); err != nil {
				return nil, fmt.Errorf("%w: credentials %s: %w", ErrConfig, path, err)
			}
		}
	}
	maps.Copy(merged, process)
	return merged, nil
}

// Logger returns a text (or JSON) logger at level ("debug", "info",
// "warn", "error"; empty means "warn"). A nil writer discards.
func Logger(w io.Writer, level string, json bool) (*slog.Logger, error) {
	if w == nil {
		return slog.New(slog.DiscardHandler), nil
	}
	var l slog.Level
	if level == "" {
		level = "warn"
	}
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("%w: log level %q", ErrConfig, level)
	}
	o := &slog.HandlerOptions{Level: l}
	if json {
		return slog.New(slog.NewJSONHandler(w, o)), nil
	}
	return slog.New(slog.NewTextHandler(w, o)), nil
}

// MaxActiveDevices bounds parallel device sessions.
const MaxActiveDevices = 500

// ErrDescriptors reports a file-descriptor limit too low for the requested
// parallelism.
var ErrDescriptors = errors.New("bootstrap: insufficient file descriptors")

// RequiredDescriptors estimates the descriptors needed for active parallel
// SSH sessions: each uses a PTY pair, pipes and the ssh process's files.
func RequiredDescriptors(active int) uint64 { return uint64(128 + 16*active) }

// limitMu serializes changes of the process-wide RLIMIT_NOFILE, which is
// itself global state.
var limitMu sync.Mutex

// PrepareFileLimit raises the soft descriptor limit toward the hard limit
// for active parallel sessions, or reports ErrDescriptors.
func PrepareFileLimit(ctx context.Context, active int) error {
	return prepare(ctx, active, getLimit, setLimit)
}

func prepare(ctx context.Context, active int, get func() (cur, hard uint64, err error), set func(cur, hard uint64) error) error {
	if active < 0 || active > MaxActiveDevices {
		return fmt.Errorf("%w: %d active devices", ErrConfig, active)
	}
	if err := ctx.Err(); err != nil || active == 0 {
		return err
	}
	limitMu.Lock()
	defer limitMu.Unlock()
	required := RequiredDescriptors(active)
	cur, hard, err := get()
	if err != nil {
		return fmt.Errorf("%w: read limit: %w", ErrDescriptors, err)
	}
	if cur >= required {
		return nil
	}
	if hard < required {
		return fmt.Errorf("%w: %d sessions need %d, hard limit is %d", ErrDescriptors, active, required, hard)
	}
	if err := set(required, hard); err != nil {
		return fmt.Errorf("%w: raise limit: %w", ErrDescriptors, err)
	}
	return nil
}
