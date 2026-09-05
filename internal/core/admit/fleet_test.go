package admit

import (
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/watch"
)

// TestAnAllWithdrawnFleetIsAnObligation is ADR-039 rules 3 and 4.
func TestAnAllWithdrawnFleetIsAnObligation(t *testing.T) {
	l := watch.NewLedger()
	f := NewFleet("group-a")

	// ⚠ An EMPTY fleet is not all-withdrawn. Zero replicas is a group nobody has
	// told us about, and a vacuous truth here would raise an obligation about a
	// cluster that may be perfectly healthy.
	if f.AllWithdrawn() {
		t.Error("an empty fleet reports every replica withdrawn")
	}
	if raised, err := f.Report(l, 0); err != nil || raised {
		t.Errorf("an empty fleet raised an obligation (raised=%v, err=%v)", raised, err)
	}

	// One still joined: no obligation. Read work still has somewhere to go.
	f.Observe("node-1", StateWithdrawn)
	f.Observe("node-2", StateWithdrawn)
	f.Observe("node-3", StateJoined)
	if f.AllWithdrawn() {
		t.Error("a fleet with one joined replica reports every replica withdrawn")
	}
	if raised, err := f.Report(l, 0); err != nil || raised {
		t.Errorf("an obligation was raised while a replica was still joined (raised=%v, err=%v)",
			raised, err)
	}
	if got := l.Len(); got != 0 {
		t.Fatalf("%d obligations outstanding, want 0", got)
	}

	// The last one withdraws: read work has nowhere left to go.
	f.Observe("node-3", StateWithdrawn)
	if !f.AllWithdrawn() {
		t.Fatal("every replica has withdrawn and the fleet does not say so")
	}
	raised, err := f.Report(l, 1000)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !raised {
		t.Fatal("an all-withdrawn fleet raised no obligation")
	}

	out := l.Outstanding(2000)
	if len(out) != 1 {
		t.Fatalf("%d obligations outstanding, want 1", len(out))
	}
	if out[0].Kind != observe.KindFleetWithdrawn || out[0].Subject != "group-a" {
		t.Errorf("obligation = %+v, want a fleet-withdrawn about group-a", out[0])
	}
	// ★ It names the replicas, so an operator can act rather than only be told
	// that something is wrong.
	if got := out[0].Detail["withdrawn"]; got != "node-1, node-2, node-3" {
		t.Errorf("detail[withdrawn] = %q, want all three replicas", got)
	}
	if got := out[0].Detail["replicas"]; got != "3" {
		t.Errorf("detail[replicas] = %q, want 3", got)
	}

	// ★ THE POINT: the fleet RECOVERS and the obligation stays. A cluster that
	// shed everything and came back is exactly what somebody should still see —
	// silence is not resolution, and only an acknowledgement clears it.
	f.Observe("node-1", StateJoined)
	if f.AllWithdrawn() {
		t.Fatal("the fleet still reports all-withdrawn after a replica rejoined")
	}
	if raised, err := f.Report(l, 3000); err != nil || raised {
		t.Errorf("a recovered fleet raised another obligation (raised=%v, err=%v)", raised, err)
	}
	if got := l.Len(); got != 1 {
		t.Errorf("%d obligations outstanding after recovery, want 1 — recovery does not "+
			"resolve what happened", got)
	}
	if err := l.Acknowledge(observe.KindFleetWithdrawn, "group-a", "operator", 4000); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if got := l.Len(); got != 0 {
		t.Errorf("%d outstanding after acknowledgement, want 0", got)
	}
}

// TestTheFleetCannotChangeANodesOwnDecision is ADR-039 rule 1.
//
// ⚠ The fleet is deliberately BUILT and QUERIED here, so its irrelevance is
// asserted rather than merely unexercised. A test that simply omitted it would
// prove the fleet is unused by omission, and omission is exactly what a later
// "group-aware admission" change would undo.
func TestTheFleetCannotChangeANodesOwnDecision(t *testing.T) {
	f := NewFleet("group-a")
	f.Observe("node-1", StateWithdrawn)
	f.Observe("node-2", StateWithdrawn)
	if !f.AllWithdrawn() {
		t.Fatal("the fixture does not have every peer withdrawn, so it tests nothing")
	}

	// This node is saturated. Every one of its peers has already withdrawn, which
	// is precisely the situation in which a group-aware rule would keep it
	// joined — and that is the trap: it would keep taking work it cannot serve.
	c := mustController(t)
	state, _ := at(t, c, 0.99)
	if state != StateWithdrawn {
		t.Fatalf("a node at 99%% utilisation is %v while all its peers are withdrawn, want "+
			"withdrawn.\nA node that stays joined because its peers left keeps taking work it "+
			"cannot serve, which is the error-returning behaviour ADR-015 rejected.", state)
	}
	if c.Admits(KindRead, ClassUser) {
		t.Error("the saturated node still admits user reads because its peers withdrew")
	}

	// ★ And the reverse: a healthy node stays joined even though its peers are
	// all withdrawn. The fleet's condition reaches its decision in neither
	// direction, because there is no path between them.
	healthy := mustController(t)
	if state, _ := at(t, healthy, 0.10); state != StateJoined {
		t.Errorf("a node at 10%% utilisation is %v while all its peers are withdrawn, want "+
			"joined — a peer's saturation is not this node's", state)
	}

	// The fleet still says what it said; consulting it changed nothing anywhere.
	if !f.AllWithdrawn() {
		t.Error("querying the fleet changed what it reports")
	}
}
