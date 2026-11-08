//go:build !windows

package lock

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

type Lock struct{ f *os.File }

func Acquire(path string) (*Lock, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another sync may be running: %w", e)
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error { _ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN); return l.f.Close() }
