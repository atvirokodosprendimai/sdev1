package hlc

import (
	"errors"
	"testing"
	"time"
)

const secondNanos = int64(time.Second)

// TestARefusedRemoteLeavesTheClockUntouched is ADR-042's falsifier.
//
// ⚠ The assertion is on the CLOCK, not on the error. Checking the skew AFTER
// merging and then returning an error looks identical from a caller's side — and
// it is exactly the defect, because the damage is the absorption. Monotonicity is
// the property that forbids coming back.
func TestARefusedRemoteLeavesTheClockUntouched(t *testing.T) {
	const localWall = int64(1_000_000)
	c := NewClock(func() int64 { return localWall })

	// Establish a known state.
	before := c.Now()
	if before.Wall != localWall {
		t.Fatalf("first reading = %v, want wall %d", before, localWall)
	}

	// A remote an hour ahead, against a one-second bound.
	bound := Bound{MaxSkew: secondNanos}
	remote := Timestamp{Wall: localWall + time.Hour.Nanoseconds()}

	got, err := c.Admit(remote, bound)
	if !errors.Is(err, ErrSkewTooLarge) {
		t.Fatalf("Admit(an hour ahead) = %v, %v; want ErrSkewTooLarge", got, err)
	}

	// ★ THE POINT: the clock did not move. An implementation that merged and then
	// reported would pass every assertion about the error and fail this one.
	if after := c.Last(); after != before {
		t.Fatalf("a refused remote moved the clock from %v to %v.\n"+
			"The merge is irreversible — monotonicity is the property that forbids coming "+
			"back — so a check performed afterwards reports damage rather than preventing "+
			"it, and the cluster cannot recover.", before, after)
	}

	// And the next reading follows the ORIGINAL state rather than the rejected
	// one, which is what "untouched" has to mean downstream.
	next := c.Now()
	if next.Wall != localWall {
		t.Errorf("the reading after a refusal has wall %d, want %d — the rejected remote "+
			"leaked into the clock", next.Wall, localWall)
	}
	if next.Compare(remote) >= 0 {
		t.Errorf("the reading after a refusal (%v) is at or past the rejected remote (%v)",
			next, remote)
	}

	// A remote inside the bound IS absorbed, so the refusal above is a decision
	// rather than a broken path.
	ok := Timestamp{Wall: localWall + secondNanos/2}
	merged, err := c.Admit(ok, bound)
	if err != nil {
		t.Fatalf("Admit(half a second ahead): %v", err)
	}
	if merged.Compare(ok) <= 0 {
		t.Errorf("an admitted remote %v did not advance the clock past it: %v", ok, merged)
	}
}

// TestSkewIsMeasuredByTheReceiver is ADR-042 rules 2 and 3.
//
// ★ Two receivers with different wall readings measure the SAME remote
// differently. That makes rule 3's honest limit visible rather than only stated:
// this measures disagreement between two clocks, not either one's error.
func TestSkewIsMeasuredByTheReceiver(t *testing.T) {
	remote := Timestamp{Wall: 10 * secondNanos}

	onTime := SkewOf(remote, 10*secondNanos)
	if onTime.Ahead != 0 {
		t.Errorf("a remote matching the receiver measures %d ahead, want 0", onTime.Ahead)
	}

	behindReceiver := SkewOf(remote, 5*secondNanos)
	if behindReceiver.Ahead != 5*secondNanos {
		t.Errorf("skew = %d, want %d", behindReceiver.Ahead, 5*secondNanos)
	}

	// ⚠ The SAME remote, against a receiver whose own clock is ahead, measures
	// as BEHIND. Neither receiver is consulting the sender about its error.
	aheadReceiver := SkewOf(remote, 20*secondNanos)
	if aheadReceiver.Ahead != -10*secondNanos {
		t.Errorf("skew = %d, want %d", aheadReceiver.Ahead, -10*secondNanos)
	}

	// ⚠ A remote BEHIND the receiver is never refused. It cannot drag this clock
	// forward, so it is harmless to monotonicity, and refusing it would reject a
	// node whose only fault is being slow.
	bound := Bound{MaxSkew: secondNanos}
	if aheadReceiver.Exceeds(bound) {
		t.Error("a remote behind the receiver was refused; it cannot drag the clock forward")
	}
	if !behindReceiver.Exceeds(bound) {
		t.Error("a remote five seconds ahead did not exceed a one-second bound")
	}

	// And two real clocks disagree the same way, through Admit.
	slow := NewClock(func() int64 { return 5 * secondNanos })
	fast := NewClock(func() int64 { return 20 * secondNanos })
	if _, err := slow.Admit(remote, bound); !errors.Is(err, ErrSkewTooLarge) {
		t.Errorf("the slow receiver accepted a remote five seconds ahead: %v", err)
	}
	if _, err := fast.Admit(remote, bound); err != nil {
		t.Errorf("the fast receiver refused a remote BEHIND it: %v", err)
	}
}

// TestABoundIsRequired is ADR-042 rule 6, and the boundary condition.
func TestABoundIsRequired(t *testing.T) {
	c := NewClock(func() int64 { return 1000 })
	remote := Timestamp{Wall: 1000}

	for _, b := range []Bound{{}, {MaxSkew: 0}, {MaxSkew: -1}} {
		if _, err := c.Admit(remote, b); !errors.Is(err, ErrNoBound) {
			t.Errorf("Admit with bound %+v = %v, want ErrNoBound.\n"+
				"A datacentre and a wide-area link tolerate different skew, so a default "+
				"would be a number nobody chose that is wrong somewhere.", b, err)
		}
	}

	// ⚠ A remote exactly AT the bound is ACCEPTED. An off-by-one the other way
	// refuses honest peers at precisely the tolerance an operator declared.
	const localWall = int64(1000)
	at := NewClock(func() int64 { return localWall })
	bound := Bound{MaxSkew: 500}
	if _, err := at.Admit(Timestamp{Wall: localWall + 500}, bound); err != nil {
		t.Errorf("a remote exactly at the bound was refused: %v", err)
	}
	past := NewClock(func() int64 { return localWall })
	if _, err := past.Admit(Timestamp{Wall: localWall + 501}, bound); !errors.Is(err, ErrSkewTooLarge) {
		t.Errorf("a remote one nanosecond past the bound was accepted: %v", err)
	}
}

// TestHistoryFromStorageIsStillMergeable is ADR-042 rule 4.
//
// ⚠ Asserted explicitly, because "apply the bound everywhere" is the tidier rule
// and it is the dangerous one: it makes a leaf written by a formerly-skewed node
// permanently unreadable, converting a clock problem into data loss over skew
// that already happened.
func TestHistoryFromStorageIsStillMergeable(t *testing.T) {
	const localWall = int64(1_000_000)
	c := NewClock(func() int64 { return localWall })

	// A timestamp far beyond any bound anyone would declare — as a leaf written
	// by a node whose clock was wrong would carry.
	stored := Timestamp{Wall: localWall + (24 * time.Hour).Nanoseconds()}

	// Admit refuses it, which is right at a network boundary.
	if _, err := c.Admit(stored, Bound{MaxSkew: secondNanos}); !errors.Is(err, ErrSkewTooLarge) {
		t.Fatalf("Admit accepted a day-ahead remote: %v", err)
	}

	// ★ Merge does not, which is right for history read back from a disk. The
	// skew already happened; refusing now would punish the reader and make
	// committed data unreachable.
	merged := c.Merge(stored)
	if merged.Compare(stored) <= 0 {
		t.Fatalf("Merge(%v) = %v; stored history must still be absorbed, or a leaf written "+
			"by a formerly-skewed node becomes permanently unreadable", stored, merged)
	}
	if c.Last().Wall != stored.Wall {
		t.Errorf("after merging stored history the clock is at %v, want wall %d",
			c.Last(), stored.Wall)
	}
}
