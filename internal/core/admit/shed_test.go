package admit

import (
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func testLeaf() addr.LeafID {
	var l addr.LeafID
	l.Prefix[0] = 0x51
	l.Depth = 1
	return l
}

// at drives the read budget to a utilisation and re-decides.
func at(t *testing.T, c *Controller, utilisation float64) (State, bool) {
	t.Helper()
	if err := c.Observe(KindRead, utilisation*c.Budget(KindRead).Ceiling.BitsPerSecond); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return c.Decide()
}

// TestNodeBetweenThresholdsKeepsItsState is the hysteresis, which is the whole
// mechanism.
//
// ⚠ It samples INSIDE the band from BOTH starting states. Sampling only above
// and only below proves nothing about the band, and the band is the only place
// where having two thresholds differs from having one.
func TestNodeBetweenThresholdsKeepsItsState(t *testing.T) {
	if got := len(States()); got != 2 {
		t.Fatalf("there are %d states, want exactly 2", got)
	}

	// Withdraw at 0.90, rejoin at 0.70. The band is (0.70, 0.90].
	for _, inBand := range []float64{0.71, 0.75, 0.80, 0.85, 0.90} {
		// Starting joined, it stays joined.
		joined := mustController(t)
		if joined.State() != StateJoined {
			t.Fatalf("a fresh controller is %v, want joined", joined.State())
		}
		if state, changed := at(t, joined, inBand); state != StateJoined || changed {
			t.Errorf("at %v a joined node became %v (changed=%v); inside the band it must keep "+
				"its state, and that band IS the hysteresis", inBand, state, changed)
		}

		// Starting withdrawn, it stays withdrawn — the direction that matters,
		// because a single-threshold decision would rejoin here and flap.
		withdrawn := mustController(t)
		if state, _ := at(t, withdrawn, 0.99); state != StateWithdrawn {
			t.Fatalf("the node did not withdraw at 0.99")
		}
		if state, changed := at(t, withdrawn, inBand); state != StateWithdrawn || changed {
			t.Errorf("at %v a withdrawn node became %v (changed=%v); a one-threshold decision "+
				"would rejoin here at exactly the load that made it leave", inBand, state, changed)
		}
	}

	// Outside the band it does move, so the test above is about the band rather
	// than about a decision that never changes anything.
	c := mustController(t)
	if state, changed := at(t, c, 0.95); state != StateWithdrawn || !changed {
		t.Errorf("at 0.95 the node is %v (changed=%v), want withdrawn", state, changed)
	}
	if state, changed := at(t, c, 0.50); state != StateJoined || !changed {
		t.Errorf("at 0.50 the node is %v (changed=%v), want joined", state, changed)
	}
}

// TestRisingAndFallingLoadDoNotFlap checks a realistic load profile produces one
// transition rather than one per sample.
func TestRisingAndFallingLoadDoNotFlap(t *testing.T) {
	c := mustController(t)

	// Rising past the withdraw point, then settling back INTO the band — the
	// shape that flaps under a single threshold.
	profile := []float64{0.60, 0.70, 0.80, 0.88, 0.92, 0.89, 0.85, 0.88, 0.91, 0.87, 0.80, 0.75}

	transitions := 0
	for _, u := range profile {
		if _, changed := at(t, c, u); changed {
			transitions++
		}
	}
	if transitions != 1 {
		t.Errorf("a profile that crosses the threshold once and settles in the band produced %d "+
			"transitions, want 1 — the flapping costs more than the load did", transitions)
	}
	if c.State() != StateWithdrawn {
		t.Errorf("the node ended %v; it never fell below the rejoin threshold", c.State())
	}

	// It does come back once load actually drops below the rejoin point.
	if _, changed := at(t, c, 0.65); !changed || c.State() != StateJoined {
		t.Errorf("the node did not rejoin at 0.65 (below the 0.70 rejoin threshold)")
	}

	// A profile that never crosses anything produces no transitions, so the
	// count above is about crossings rather than about samples.
	quiet := mustController(t)
	for _, u := range []float64{0.10, 0.20, 0.30, 0.40} {
		if _, changed := at(t, quiet, u); changed {
			t.Errorf("a quiet profile produced a transition at %v", u)
		}
	}
}

// TestWithdrawalStopsOnlyNewReadWork checks what withdrawal does and does not
// touch.
func TestWithdrawalStopsOnlyNewReadWork(t *testing.T) {
	c := mustController(t)
	if err := c.Observe(KindWrite, 500_000_000); err != nil {
		t.Fatalf("Observe(write): %v", err)
	}
	writeBefore := c.Budget(KindWrite).Utilisation()

	if state, _ := at(t, c, 0.99); state != StateWithdrawn {
		t.Fatal("the node did not withdraw")
	}

	if c.Admits(KindRead) {
		t.Error("a withdrawn node still admits reads")
	}
	if !c.Admits(KindWrite) {
		t.Error("a withdrawn node stopped admitting writes")
	}
	if got := c.Budget(KindWrite).Utilisation(); got != writeBefore {
		t.Errorf("withdrawal changed write utilisation from %v to %v", writeBefore, got)
	}
	// The read budget's own accounting is untouched by the state change; only
	// the pull of new work stopped.
	if got := c.Budget(KindRead).Utilisation(); got < 0.98 {
		t.Errorf("withdrawal reset read utilisation to %v; it must reflect what is happening, "+
			"not what the node has decided about it", got)
	}
}

// TestWriteBudgetNeverWithdraws checks the write side has no withdrawn state to
// enter, whatever its load.
func TestWriteBudgetNeverWithdraws(t *testing.T) {
	c := mustController(t)

	for _, rate := range []float64{0, 0.5e9, 0.95e9, 5e9, 50e9} {
		if err := c.Observe(KindWrite, rate); err != nil {
			t.Fatalf("Observe(write, %v): %v", rate, err)
		}
		c.Decide()
		if !c.Admits(KindWrite) {
			t.Fatalf("at a write rate of %v the node stopped admitting writes; a leaf has one "+
				"writer, so a shed write is an outage rather than a re-route", rate)
		}
	}

	// And the state machine is driven by the READ budget alone: a saturated
	// write budget with an idle read budget leaves the node joined.
	idle := mustController(t)
	if err := idle.Observe(KindWrite, 50e9); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := idle.Observe(KindRead, 0); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if state, changed := idle.Decide(); state != StateJoined || changed {
		t.Errorf("write saturation moved the node to %v (changed=%v); shedding is a read "+
			"mechanism and write load must not drive it", state, changed)
	}
}

// TestStateChangesAreDeclaredEvents checks the two kinds live in the closed
// vocabulary, and that an event marks a transition rather than an evaluation.
func TestStateChangesAreDeclaredEvents(t *testing.T) {
	for _, k := range []observe.Kind{observe.KindQueueWithdrawn, observe.KindQueueRejoined} {
		d, ok := observe.DeclarationFor(k)
		if !ok {
			t.Fatalf("kind %q is not declared; ADR-012's vocabulary is closed, so an ad-hoc "+
				"emission would be refused", k)
		}
		if d.Reader == "" {
			t.Errorf("kind %q declares no reader", k)
		}
	}

	c := mustController(t)
	leaf, id := testLeaf(), tx.TxID{}

	// A transition produces an event.
	if err := c.Observe(KindRead, 0.99*1e9); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	ev, emitted, err := c.Observed(leaf, id, "node-a")
	if err != nil {
		t.Fatalf("Observed: %v", err)
	}
	if !emitted {
		t.Fatal("crossing the withdraw threshold produced no event")
	}
	if ev.Kind != observe.KindQueueWithdrawn {
		t.Errorf("event kind = %q, want %q", ev.Kind, observe.KindQueueWithdrawn)
	}
	if ev.Fields["node"] != "node-a" {
		t.Errorf("the event does not name the node: %v", ev.Fields)
	}
	if ev.Fields["utilisation"] == "" || ev.Fields["threshold"] == "" {
		t.Errorf("the event omits what was measured against what: %v", ev.Fields)
	}

	// ⚠ A re-evaluation that changes nothing produces NO event. Emitting per
	// evaluation would bury the handful of transitions an operator wants in a
	// stream of non-events.
	if _, emitted, err := c.Observed(leaf, id, "node-a"); err != nil || emitted {
		t.Errorf("a re-evaluation with no state change emitted an event (err=%v)", err)
	}

	// Falling back produces the paired kind.
	if err := c.Observe(KindRead, 0.10*1e9); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	back, emitted, err := c.Observed(leaf, id, "node-a")
	if err != nil {
		t.Fatalf("Observed: %v", err)
	}
	if !emitted || back.Kind != observe.KindQueueRejoined {
		t.Errorf("rejoining produced (%v, emitted=%v), want a rejoined event", back.Kind, emitted)
	}
}
