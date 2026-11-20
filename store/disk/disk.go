package disk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/renameio/v2"
)

// ErrUnsafe reports a file or directory that is not private to the current
// user, is not of the expected kind, or exceeds a bound.
var ErrUnsafe = errors.New("disk: unsafe file")

// Receipt reports how far a replacement progressed. A failed write may
// still be Committed: the new content is visible but the directory entry
// was not synchronized.
type Receipt struct {
	Path      string `json:"path"`
	Committed bool   `json:"committed"`
	Durable   bool   `json:"durable"`
}

// WriteFile atomically replaces path with body (mode 0600) and synchronizes
// the parent directory. The parent must exist.
func WriteFile(ctx context.Context, path string, body []byte) (Receipt, error) {
	r := Receipt{Path: path}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if err := renameio.WriteFile(path, body, 0o600); err != nil {
		return r, fmt.Errorf("disk: replace %s: %w", filepath.Base(path), err)
	}
	r.Committed = true
	if err := SyncDir(filepath.Dir(path)); err != nil {
		return r, err
	}
	r.Durable = true
	return r, nil
}

// ReadFile reads a regular file of at most limit bytes and returns its
// SHA-256 in hex. A missing file wraps fs.ErrNotExist.
func ReadFile(ctx context.Context, path string, limit int64) (body []byte, sha string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%w: %s is not a regular file", ErrUnsafe, filepath.Base(path))
	}
	if info.Size() > limit {
		return nil, "", fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafe, filepath.Base(path), limit)
	}
	body, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		return nil, "", fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafe, filepath.Base(path), limit)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), ctx.Err()
}

// OpenAppend opens (creating with mode 0600) a private append-only file. It
// refuses symbolic links and files that others can read or write.
func OpenAppend(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrUnsafe, filepath.Base(path))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.Join(fmt.Errorf("%w: %s is not private", ErrUnsafe, filepath.Base(path)), err, f.Close())
	}
	return f, nil
}

// PrepareDir creates dir and its missing parents with mode 0700 and checks
// that dir is a directory owned by the current user and not writable by
// others.
func PrepareDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return checkPrivate(dir)
}

// SyncDir flushes the directory entry list of dir.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(syncDir(d), d.Close())
}

// Lock acquires the exclusive cross-process lock file at path, waiting up
// to timeout (zero means five seconds). Close releases it.
func Lock(ctx context.Context, path string, timeout time.Duration) (io.Closer, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	l := flock.New(path, flock.SetPermissions(0o600))
	ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
	switch {
	case err != nil:
		return nil, fmt.Errorf("disk: lock %s: %w", filepath.Base(path), err)
	case !ok:
		return nil, fmt.Errorf("disk: lock %s: %w", filepath.Base(path), context.DeadlineExceeded)
	}
	return l, nil
}

// Lock satisfies io.Closer through Close; the compile-time check keeps the
// flock dependency honest.
var _ io.Closer = (*flock.Flock)(nil)
