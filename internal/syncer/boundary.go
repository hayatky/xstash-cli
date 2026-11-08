// Package syncer implements synchronization decisions independently of the X transport.
package syncer

// Boundary tracks consecutive previously known relations. Initial sync never
// stops at a known relation because prior runs may have failed partway through.
type Boundary struct {
	Initial   bool
	Threshold int
	streak    int
}

func (b *Boundary) Observe(known bool) bool {
	if b.Initial || b.Threshold < 1 {
		return false
	}
	if known {
		b.streak++
	} else {
		b.streak = 0
	}
	return b.streak >= b.Threshold
}
