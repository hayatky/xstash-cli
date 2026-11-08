package lock

import (
	"path/filepath"
	"testing"
)

func TestExclusiveAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	a, e := Acquire(path)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := Acquire(path); e == nil {
		b.Close()
		t.Fatal("second lock acquired")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := Acquire(path)
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
}
