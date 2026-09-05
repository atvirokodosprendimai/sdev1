package durability

import (
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/watch"
)

const (
	second = int64(time.Second)
	minute = int64(time.Minute)
)

func leafAt(b byte) addr.LeafID {
	var l addr.LeafID
	l.Prefix[0] = b
	l.Depth = 1
	return l
}

// threeRacks is a policy needing copies in three distinct racks.
func threeRacks(t *testing.T) Policy {
	t.Helper()
	p, err := Replicated(3, 3, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	return p
}

// TestARestartAndAShortfallAreToldApartByAgeAlone is ADR-040's falsifier.
//
// ★ The two leaves are IDENTICAL — same policy, same domain count, same shortfall
// — and differ in one thing only: how long it lasts. If the fixtures differed in
// any other way, the test would show that SOMETHING distinguishes them rather
// than that TIME does, which is the claim.
func TestARestartAndAShortfallAreToldApartByAgeAlone(t *testing.T) {
	p := threeRacks(t)
	w, err := NewWatchdog(30 * second)
	if err != nil {
		t.Fatalf("NewWatchdog: %v", err)
	}
	l := watch.NewLedger()

	restarting := leafAt(0x11)
	failing := leafAt(0x22)

	// Both drop to two racks at the same instant, identically.
	const t0 = int64(1000)
	twoRacks := []string{"rack-a", "rack-b"}
	w.Observe(restarting, p, twoRacks, t0)
	w.Observe(failing, p, twoRacks, t0)

	if got := len(w.Status(t0)); got != 2 {
		t.Fatalf("%d leaves short, want 2 — the fixtures are not identical", got)
	}

	// Ten seconds in, inside the grace: neither is anybody's problem yet.
	if raised, err := w.Report(l, t0+10*second); err != nil || raised != 0 {
		t.Fatalf("inside the grace, raised %d obligation(s) (err %v), want 0", raised, err)
	}

	// The restarting leaf comes back. The failing one does not.
	w.Observe(restarting, p, []string{"rack-a", "rack-b", "rack-c"}, t0+20*second)

	// Past the grace.
	now := t0 + 5*minute
	raised, err := w.Report(l, now)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if raised != 1 {
		t.Fatalf("raised %d obligation(s), want exactly 1.\n"+
			"Instantaneously a restart and a shortfall are the SAME observation — two racks of "+
			"three, whatever the reason. Only how long it lasts can separate them.", raised)
	}

	out := l.Outstanding(now)
	if len(out) != 1 {
		t.Fatalf("%d outstanding, want 1", len(out))
	}
	if out[0].Kind != observe.KindLeafBelowFloor || out[0].Subject != failing.String() {
		t.Errorf("obligation = %+v, want a below-floor about the FAILING leaf", out[0])
	}
	// ★ It names what an operator needs to act: how many domains, and how many
	// the policy wanted.
	if out[0].Detail["domains"] != "2" || out[0].Detail["required"] != "3" {
		t.Errorf("detail = %v, want 2 domains against a required 3", out[0].Detail)
	}
	// The obligation's age is the leaf's real age, not the age of the report.
	if out[0].Age != now-t0 {
		t.Errorf("obligation age = %d, want %d — it dates from when the leaf went short, "+
			"not from when somebody looked", out[0].Age, now-t0)
	}

	// The recovered leaf is gone from the status, and never became an obligation.
	for _, s := range w.Status(now) {
		if s.Leaf == restarting {
			t.Error("the recovered leaf is still reported as short")
		}
	}
}

// TestTheStatusDoesNotHideBehindTheGrace is ADR-040 rule 4.
//
// ⚠ Both halves in ONE test: the status showing AND the obligation withheld. That
// pair is the whole of the rule, and either alone is a different rule — "suppress
// for N seconds" satisfies the second and breaks the first.
func TestTheStatusDoesNotHideBehindTheGrace(t *testing.T) {
	p := threeRacks(t)
	w, err := NewWatchdog(30 * second)
	if err != nil {
		t.Fatalf("NewWatchdog: %v", err)
	}
	l := watch.NewLedger()

	leaf := leafAt(0x33)
	const t0 = int64(500)
	w.Observe(leaf, p, []string{"rack-a", "rack-b"}, t0)

	// One second in. Well inside the grace.
	now := t0 + second
	status := w.Status(now)
	if len(status) != 1 {
		t.Fatalf("%d leaves in the status one second in, want 1.\n"+
			"The grace withholds the OBLIGATION, never the status — otherwise it is a window "+
			"in which a genuine shortfall is invisible, and an operator cannot watch a restart "+
			"dip and recover.", len(status))
	}
	if status[0].Age != second {
		t.Errorf("age = %d, want %d", status[0].Age, second)
	}
	if status[0].Domains != 2 || status[0].Policy.MinSize != 3 {
		t.Errorf("status = %+v, want 2 domains against a required 3", status[0])
	}
	if status[0].Reportable(w.Grace()) {
		t.Error("a one-second shortfall is reportable; the grace is 30 seconds")
	}

	// And nobody is answerable for it yet.
	if raised, err := w.Report(l, now); err != nil || raised != 0 {
		t.Fatalf("raised %d obligation(s) inside the grace (err %v), want 0", raised, err)
	}
	if got := l.Len(); got != 0 {
		t.Errorf("%d obligations outstanding inside the grace, want 0", got)
	}

	// Past the grace, the same leaf becomes an obligation without anything else
	// changing — so the grace is the only thing that was withholding it.
	if raised, _ := w.Report(l, t0+31*second); raised != 1 {
		t.Errorf("raised %d obligation(s) past the grace, want 1", raised)
	}
}

// TestABelowFloorLeafStaysReadable is ADR-040 rule 1.
//
// ⚠ Asserted rather than left unexercised. "Evict it" is the response that feels
// safest and is the one that turns a durability risk into a certain outage — and
// it removes exactly the copies that still exist.
func TestABelowFloorLeafStaysReadable(t *testing.T) {
	p := threeRacks(t)
	w, err := NewWatchdog(second)
	if err != nil {
		t.Fatalf("NewWatchdog: %v", err)
	}
	l := watch.NewLedger()

	leaf := leafAt(0x44)
	const t0 = int64(0)
	w.Observe(leaf, p, []string{"rack-a"}, t0)

	// Long past the grace, and reported.
	now := t0 + time.Hour.Nanoseconds()
	if raised, err := w.Report(l, now); err != nil || raised != 1 {
		t.Fatalf("raised %d (err %v), want 1", raised, err)
	}

	// ★ The leaf is STILL in the status after being reported. Nothing removed it,
	// disabled it, or took it out of service — the watchdog reports and does not
	// act.
	status := w.Status(now)
	if len(status) != 1 || status[0].Leaf != leaf {
		t.Fatalf("after reporting, the status holds %+v; a below-floor leaf is degraded, "+
			"not wrong, and its data is still readable and correct", status)
	}

	// Reporting twice does not duplicate the obligation, and does not remove the
	// leaf either.
	if _, err := w.Report(l, now+second); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if got := l.Len(); got != 1 {
		t.Errorf("%d obligations after reporting twice, want 1", got)
	}
	if len(w.Status(now+second)) != 1 {
		t.Error("reporting removed the leaf from the status")
	}

	// ⚠ And recovery leaves the obligation standing: a leaf that fell below its
	// floor and came back is exactly what an operator should still see.
	w.Observe(leaf, p, []string{"rack-a", "rack-b", "rack-c"}, now+2*second)
	if len(w.Status(now+2*second)) != 0 {
		t.Error("a recovered leaf is still in the status")
	}
	if got := l.Len(); got != 1 {
		t.Errorf("recovery cleared the obligation (%d outstanding); only an acknowledgement "+
			"does that", got)
	}
}

// TestAWatchdogWithNoGraceIsRefused is ADR-040 rule 6.
func TestAWatchdogWithNoGraceIsRefused(t *testing.T) {
	for _, grace := range []int64{0, -1, -second} {
		if _, err := NewWatchdog(grace); !errors.Is(err, ErrNoGrace) {
			t.Errorf("NewWatchdog(%d) = %v, want ErrNoGrace.\n"+
				"The grace is the entire discriminator, so a watchdog without one is not one "+
				"with a default — it cannot answer the question it exists for.", grace, err)
		}
	}
	if _, err := NewWatchdog(second); err != nil {
		t.Errorf("a one-second grace was refused: %v", err)
	}
}
