//go:build !unix

package disk

import (
	"fmt"
	"os"
)

func checkPrivate(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafe, dir)
	}
	return nil
}

// Directories cannot be synchronized on this platform.
func syncDir(*os.File) error { return nil }
