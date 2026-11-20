//go:build unix

package disk

import (
	"fmt"
	"os"
	"syscall"
)

func checkPrivate(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafe, dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is writable by others", ErrUnsafe, dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%w: %s is owned by another user", ErrUnsafe, dir)
	}
	return nil
}

func syncDir(d *os.File) error { return d.Sync() }
