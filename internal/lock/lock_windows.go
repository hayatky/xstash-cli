//go:build windows

package lock

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

type Lock struct {
	f          *os.File
	overlapped windows.Overlapped
}

func Acquire(path string) (*Lock, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	l := &Lock{f: f}
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.overlapped)
	if e != nil {
		f.Close()
		return nil, fmt.Errorf("another sync may be running: %w", e)
	}
	return l, nil
}
func (l *Lock) Close() error {
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.overlapped)
	return l.f.Close()
}
