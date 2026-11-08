package syncer

import "testing"

func TestBoundaryRequiresConsecutiveKnownRelations(t *testing.T) {
	b := Boundary{Threshold: 3}
	for i, k := range []bool{true, true, false, true, true, true} {
		stop := b.Observe(k)
		if stop != (i == 5) {
			t.Fatalf("index %d: %v", i, stop)
		}
	}
	initial := Boundary{Initial: true, Threshold: 1}
	if initial.Observe(true) {
		t.Fatal("initial sync must not stop")
	}
}
func TestSmallerPagesReduceOverread(t *testing.T) {
	// Four new resources precede five known ones. The request bills the whole page.
	read := func(size int) int {
		known := []bool{false, false, false, false, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true}
		b := Boundary{Threshold: 5}
		n := 0
		for start := 0; start < len(known); start += size {
			end := start + size
			if end > len(known) {
				end = len(known)
			}
			n += end - start
			for _, k := range known[start:end] {
				if b.Observe(k) {
					return n
				}
			}
		}
		return n
	}
	if small, large := read(5), read(20); small >= large {
		t.Fatalf("small=%d large=%d", small, large)
	}
}
