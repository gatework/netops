//go:build unix

package bootstrap

import "golang.org/x/sys/unix"

func getLimit() (cur, hard uint64, err error) {
	var l unix.Rlimit
	err = unix.Getrlimit(unix.RLIMIT_NOFILE, &l)
	return uint64(l.Cur), uint64(l.Max), err //nolint:unconvert // field types differ across unix platforms
}

func setLimit(cur, hard uint64) error {
	l := unix.Rlimit{Cur: cur, Max: hard}
	return unix.Setrlimit(unix.RLIMIT_NOFILE, &l)
}
