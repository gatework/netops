package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/store/disk"
)

var (
	// ErrNotFound reports a missing file.
	ErrNotFound = errors.New("store: not found")
	// ErrUnsafe reports a path that escapes the root, or a root that other
	// users can write.
	ErrUnsafe = disk.ErrUnsafe
)

// Receipt reports how far a replacement progressed.
type Receipt = disk.Receipt

// DefaultLimit bounds reads that do not name a limit.
const DefaultLimit = 64 << 20

// Store is a private directory. It is safe for concurrent use; writers of
// the same file should hold its Lock.
type Store struct {
	root string
}

// Open creates root (mode 0700) when missing and checks that it is a
// directory owned by the current user and not writable by others.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := disk.PrepareDir(abs); err != nil {
		return nil, fmt.Errorf("store: prepare %s: %w", abs, err)
	}
	return &Store{root: abs}, nil
}

// batchZone names batch directories in the operators' local time, as the
// retired kernels did.
var batchZone = time.FixedZone("UTC+8", 8*60*60)

// NewBatch creates a new private batch directory under root, named after
// at ("2006-01-02_15-04-05", with "_01", "_02"... on collision), and
// opens it. Every run of a tool gets its own batch.
func NewBatch(root string, at time.Time) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := disk.PrepareDir(abs); err != nil {
		return nil, fmt.Errorf("store: prepare %s: %w", abs, err)
	}
	base := at.In(batchZone).Format("2006-01-02_15-04-05")
	for n := range 100 {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%02d", base, n)
		}
		dir := filepath.Join(abs, name)
		err := os.Mkdir(dir, 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store: batch: %w", err)
		}
		if err := disk.SyncDir(abs); err != nil {
			return nil, fmt.Errorf("store: batch: %w", err)
		}
		return &Store{root: dir}, nil
	}
	return nil, fmt.Errorf("store: batch: too many batches at %s", base)
}

// Root returns the absolute root directory.
func (s *Store) Root() string { return s.root }

// Path resolves a slash-separated relative path inside the root. It rejects
// absolute paths, "..", empty and dot-only elements.
func (s *Store) Path(rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("%w: %q", ErrUnsafe, rel)
	}
	for part := range strings.SplitSeq(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafe, rel)
		}
	}
	return filepath.Join(s.root, filepath.FromSlash(rel)), nil
}

// Write atomically replaces the file at rel, creating private parent
// directories. The receipt tells how far a failed write progressed.
func (s *Store) Write(ctx context.Context, rel string, body []byte) (Receipt, error) {
	path, err := s.Path(rel)
	if err != nil {
		return Receipt{}, err
	}
	if err := disk.PrepareDir(filepath.Dir(path)); err != nil {
		return Receipt{}, fmt.Errorf("store: prepare %s: %w", rel, err)
	}
	r, err := disk.WriteFile(ctx, path, body)
	if err != nil {
		return r, fmt.Errorf("store: write %s: %w", rel, err)
	}
	return r, nil
}

// WriteJSON writes v as indented JSON.
func (s *Store) WriteJSON(ctx context.Context, rel string, v any) (Receipt, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Receipt{}, fmt.Errorf("store: encode %s: %w", rel, err)
	}
	return s.Write(ctx, rel, append(body, '\n'))
}

// Read returns the file at rel, refusing files over limit bytes (0 means
// DefaultLimit). A missing file is ErrNotFound.
func (s *Store) Read(ctx context.Context, rel string, limit int64) ([]byte, error) {
	path, err := s.Path(rel)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	body, _, err := disk.ReadFile(ctx, path, limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", rel, err)
	}
	return body, nil
}

// ReadJSON decodes the file at rel into v, rejecting unknown fields.
func (s *Store) ReadJSON(ctx context.Context, rel string, limit int64, v any) error {
	body, err := s.Read(ctx, rel, limit)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("store: decode %s: %w", rel, err)
	}
	return nil
}

// List returns the sorted names in the directory rel ("" for the root).
// A missing directory lists as empty.
func (s *Store) List(rel string) ([]string, error) {
	dir := s.root
	if rel != "" {
		var err error
		if dir, err = s.Path(rel); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: list %s: %w", rel, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out, nil
}

// Remove deletes the file or directory tree at rel. A missing path is not
// an error.
func (s *Store) Remove(rel string) error {
	path, err := s.Path(rel)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("store: remove %s: %w", rel, err)
	}
	return nil
}

// Lock acquires the cross-process lock of key, waiting up to timeout. The
// lock files live in ".locks".
func (s *Store) Lock(ctx context.Context, key string, timeout time.Duration) (io.Closer, error) {
	path, err := s.Path(".locks/" + Name(key) + ".lock")
	if err != nil {
		return nil, err
	}
	if err := disk.PrepareDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("store: prepare locks: %w", err)
	}
	l, err := disk.Lock(ctx, path, timeout)
	if err != nil {
		return nil, fmt.Errorf("store: lock %s: %w", key, err)
	}
	return l, nil
}

// Name returns a file-name-safe form of key: a readable prefix of at most
// 48 characters from [A-Za-z0-9._-], then "-" and 12 hex digits of the
// key's SHA-256, so distinct keys never collide.
func Name(key string) string {
	prefix := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, key)
	prefix = strings.Trim(prefix, "._")
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	sum := sha256.Sum256([]byte(key))
	if prefix == "" {
		return hex.EncodeToString(sum[:6])
	}
	return prefix + "-" + hex.EncodeToString(sum[:6])
}
