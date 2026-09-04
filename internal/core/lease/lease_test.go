package lease

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

func leafAt(prefix ...byte) addr.LeafID {
	var l addr.LeafID
	copy(l.Prefix[:], prefix)
	l.Depth = uint8(len(prefix))
	return l
}

func mustGrant(t *testing.T, r *Registry, leaf addr.LeafID, holder string) Lease {
	t.Helper()
	l, err := r.Grant(leaf, holder)
	if err != nil {
		t.Fatalf("Grant(%s, %q): %v", leaf, holder, err)
	}
	return l
}

// TestEpochOnlyEverIncreases checks the ordering the whole design rests on.
//
// ⚠ It grants CONCURRENTLY as well as sequentially. A sequential test would pass
// for a counter with a data race, and the fence runs the detector — but a racy
// counter can also produce duplicates without racing detectably on every run, so
// distinctness is asserted too.
func TestEpochOnlyEverIncreases(t *testing.T) {
	r := NewRegistry()
	leaf := leafAt(0x01)

	// Sequential: strictly increasing, not merely non-decreasing. An equal epoch
	// orders nothing, which is precisely the case fencing must not permit.
	prev := NoEpoch
	for i := 0; i < 100; i++ {
		l := mustGrant(t, r, leaf, fmt.Sprintf("holder-%d", i))
		if l.Epoch <= prev {
			t.Fatalf("grant %d returned epoch %d after %d; epochs must STRICTLY increase, "+
				"because two holders at one epoch are indistinguishable to the resource", i, l.Epoch, prev)
		}
		prev = l.Epoch
	}
	if prev == NoEpoch {
		t.Fatal("no epoch was ever granted")
	}

	// Concurrent: every epoch distinct, and the highest equals the count.
	conc := NewRegistry()
	concLeaf := leafAt(0x02)
	const grants = 200
	var wg sync.WaitGroup
	seen := make([]Epoch, grants)
	for i := 0; i < grants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := conc.Grant(concLeaf, fmt.Sprintf("h%d", i))
			if err != nil {
				t.Errorf("concurrent grant %d: %v", i, err)
				return
			}
			seen[i] = l.Epoch
		}(i)
	}
	wg.Wait()

	distinct := map[Epoch]bool{}
	var highest Epoch
	for i, e := range seen {
		if e == NoEpoch {
			t.Fatalf("concurrent grant %d returned the zero epoch", i)
		}
		if distinct[e] {
			t.Fatalf("epoch %d was granted twice; two writers would be indistinguishable", e)
		}
		distinct[e] = true
		if e > highest {
			highest = e
		}
	}
	if int(highest) != grants {
		t.Errorf("after %d concurrent grants the highest epoch is %d, want %d — some grant "+
			"reused a value", grants, highest, grants)
	}
}

// TestGrantDoesNotWaitForThePreviousHolder checks a dead writer is never a
// permanent outage.
//
// The previous holder still exists here, has done nothing to release, and is
// never asked. Waiting for it is what would make its death permanent; the epoch
// is what makes not waiting safe.
func TestGrantDoesNotWaitForThePreviousHolder(t *testing.T) {
	r := NewRegistry()
	leaf := leafAt(0x03)

	first := mustGrant(t, r, leaf, "node-a")

	// node-a is still "alive" as far as anything here knows. It is not consulted,
	// not notified, and cannot object.
	second := mustGrant(t, r, leaf, "node-b")

	if second.Epoch <= first.Epoch {
		t.Fatalf("the second grant took epoch %d, not above the first's %d", second.Epoch, first.Epoch)
	}
	if second.Holder != "node-b" {
		t.Errorf("the leaf is held by %q, want node-b", second.Holder)
	}

	// And the registry offers no way to release or expire, because either would
	// have to decide whether node-a is dead — which is the decision this design
	// avoids making.
	if _, blocked := any(r).(interface{ Release(addr.LeafID) error }); blocked {
		t.Error("the registry exposes Release; it cannot distinguish a dead holder from a slow " +
			"one, so it would permit two live writers")
	}
	if _, blocked := any(r).(interface{ Expire(addr.LeafID) error }); blocked {
		t.Error("the registry exposes Expire, which is a release with a timer in front of it")
	}

	// A grant needs a holder; an anonymous lease would name nobody to fence out.
	if _, err := r.Grant(leaf, ""); err == nil {
		t.Error("a lease was granted to no holder")
	}
}

// TestEpochsAreOrderedPerLeaf checks a busy leaf does not make every other leaf's
// grants a coordination point.
func TestEpochsAreOrderedPerLeaf(t *testing.T) {
	r := NewRegistry()
	busy := leafAt(0x04)
	quiet := leafAt(0x05)

	for i := 0; i < 50; i++ {
		mustGrant(t, r, busy, "node-a")
	}
	busyNow, err := r.Current(busy)
	if err != nil {
		t.Fatalf("Current(busy): %v", err)
	}
	if busyNow.Epoch != 50 {
		t.Fatalf("the busy leaf is at epoch %d after 50 grants, want 50", busyNow.Epoch)
	}

	// The quiet leaf starts where it would have started anyway.
	q := mustGrant(t, r, quiet, "node-b")
	if q.Epoch != 1 {
		t.Errorf("the first grant on an untouched leaf took epoch %d, want 1 — epochs are "+
			"per leaf, and a global counter would make every grant a coordination point", q.Epoch)
	}

	// And the two do not interfere in either direction.
	if again := mustGrant(t, r, busy, "node-c"); again.Epoch != 51 {
		t.Errorf("the busy leaf continued at epoch %d, want 51", again.Epoch)
	}
	if again := mustGrant(t, r, quiet, "node-d"); again.Epoch != 2 {
		t.Errorf("the quiet leaf continued at epoch %d, want 2", again.Epoch)
	}
}

// TestCurrentReportsTheLatestHolder checks an operator asking who owns a leaf
// gets the answer the resource will act on.
func TestCurrentReportsTheLatestHolder(t *testing.T) {
	r := NewRegistry()
	leaf := leafAt(0x06)

	mustGrant(t, r, leaf, "node-a")
	mustGrant(t, r, leaf, "node-b")
	last := mustGrant(t, r, leaf, "node-c")

	got, err := r.Current(leaf)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got != last {
		t.Errorf("Current reports %+v, want the most recent grant %+v", got, last)
	}
	if got.Holder != "node-c" {
		t.Errorf("Current names %q, want node-c", got.Holder)
	}
	if got.Leaf != leaf {
		t.Errorf("Current reports leaf %s, want %s", got.Leaf, leaf)
	}
}

// TestNoLeaseIsRefusedByName checks an ungranted leaf says so.
//
// A zero lease would be epoch zero held by nobody: a valid-looking value that
// compares as older than everything and silently means "unowned".
func TestNoLeaseIsRefusedByName(t *testing.T) {
	r := NewRegistry()

	got, err := r.Current(leafAt(0x07))
	if !errors.Is(err, ErrNoLease) {
		t.Fatalf("an ungranted leaf: error = %v, want ErrNoLease", err)
	}
	if got != (Lease{}) {
		t.Error("a lease was returned alongside the refusal")
	}

	// Granting one makes it known, and only that one.
	mustGrant(t, r, leafAt(0x07), "node-a")
	if _, err := r.Current(leafAt(0x07)); err != nil {
		t.Errorf("after a grant: %v", err)
	}
	if _, err := r.Current(leafAt(0x08)); !errors.Is(err, ErrNoLease) {
		t.Errorf("a different leaf: error = %v, want ErrNoLease", err)
	}
}
