package admit

import "testing"

// TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair is ADR-039's falsifier.
//
// ⚠ All three tiers in one test. "A withdrawn node refuses reads" passes with the
// class ignored entirely, so only checking that REPAIR is still admitted proves
// the class does anything — and only checking that USER is refused proves the
// two have not been swapped, which is the intuitive and wrong order.
func TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair(t *testing.T) {
	c := mustController(t)

	// Joined: everything is taken.
	if state, _ := at(t, c, 0.10); state != StateJoined {
		t.Fatalf("state = %v at 10%% utilisation, want joined", state)
	}
	for _, class := range Classes() {
		if !c.Admits(KindRead, class) {
			t.Errorf("a joined node refused a %v read", class)
		}
	}

	// Withdrawn: the user read goes, the repair read stays.
	if state, _ := at(t, c, 0.99); state != StateWithdrawn {
		t.Fatalf("state = %v at 99%% utilisation, want withdrawn", state)
	}
	if c.Admits(KindRead, ClassUser) {
		t.Error("a withdrawn node still admits user reads; shedding one re-routes it to a " +
			"peer, which is what admission control is for")
	}
	if !c.Admits(KindRead, ClassRepair) {
		t.Error("a withdrawn node refused a repair read.\n" +
			"Only this node holds the fragments a repair read wants, so shedding it does not " +
			"move the work — it CANCELS it, which is the same thing that makes a shed write " +
			"an outage rather than a re-route.")
	}

	// ⚠ And a write is admitted throughout, for either class: the tier above both.
	for _, class := range Classes() {
		if !c.Admits(KindWrite, class) {
			t.Errorf("a withdrawn node refused a %v write; a leaf has one writer, so a shed "+
				"write is an outage", class)
		}
	}

	// Back below the rejoin threshold, both classes are taken again.
	if state, _ := at(t, c, 0.10); state != StateJoined {
		t.Fatalf("state = %v back at 10%%, want joined", state)
	}
	for _, class := range Classes() {
		if !c.Admits(KindRead, class) {
			t.Errorf("a rejoined node refused a %v read", class)
		}
	}

	// ★ The shed order is readable from the API rather than inferred: user first.
	if order := Classes(); len(order) != 2 || order[0] != ClassUser || order[1] != ClassRepair {
		t.Errorf("Classes() = %v, want [user repair] — first shed first", order)
	}
}

// TestWithdrawalIsDecidedFromThisNodeAlone is ADR-039 rule 1, and `BACKLOG.md`
// §22's named trap in its observable form.
//
// ★ The trap: *"a node that refuses to withdraw because its peers already have is
// a node that keeps taking work it cannot serve, which is the error-returning
// behaviour ADR-015 rejected, arrived at by a different route."*
func TestWithdrawalIsDecidedFromThisNodeAlone(t *testing.T) {
	// Two nodes at the same utilisation, reached by different histories: one has
	// been saturated all along, the other has just arrived there.
	saturated := mustController(t)
	arriving := mustController(t)

	if state, _ := at(t, saturated, 0.99); state != StateWithdrawn {
		t.Fatalf("the saturated node is %v, want withdrawn", state)
	}
	// The other node idles first, so its prior state differs.
	if state, _ := at(t, arriving, 0.10); state != StateJoined {
		t.Fatalf("the arriving node is %v, want joined", state)
	}

	// ⚠ Now both are at 99%. Whatever the other node did, this one withdraws:
	// there is no parameter through which a peer's state could reach the answer.
	if state, _ := at(t, arriving, 0.99); state != StateWithdrawn {
		t.Fatalf("a node at 99%% utilisation is %v, want withdrawn.\n"+
			"A node that stays joined because its peers have withdrawn keeps taking work it "+
			"cannot serve, which is the error-returning behaviour this package rejected.",
			state)
	}
	if saturated.State() != StateWithdrawn {
		t.Error("the first node's state changed when the second one decided")
	}

	// ★ And the enforcement is the signature: Decide() takes nothing. This call
	// compiling with no arguments is the assertion — a peer-aware version could
	// not be written without changing it, which is a visible change rather than a
	// silent one.
	if _, changed := arriving.Decide(); changed {
		t.Error("re-deciding at an unchanged utilisation reported a change")
	}

	// A node whose peers are ALL withdrawn still withdraws on its own numbers,
	// and one whose peers are all joined still withdraws too. Neither is knowable
	// here, which is the point.
	for _, u := range []float64{0.95, 0.99, 1.5} {
		fresh := mustController(t)
		if state, _ := at(t, fresh, u); state != StateWithdrawn {
			t.Errorf("at %v utilisation the node is %v, want withdrawn", u, state)
		}
	}
}

// TestBothClassesShareOneReadCeiling is ADR-039 rule 5.
//
// ⚠ A budget per class would let repair load and user load stop competing, which
// is the shared-budget failure this package exists to prevent, inverted — and
// `BACKLOG.md` §22 forbids it outright.
func TestBothClassesShareOneReadCeiling(t *testing.T) {
	c := mustController(t)
	ceiling := c.Budget(KindRead).Ceiling.BitsPerSecond

	// Load generated by a repair and load generated by a user move the SAME
	// number, because there is only one number to move.
	if err := c.Observe(KindRead, 0.5*ceiling); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	half := c.Budget(KindRead).Utilisation()
	if half < 0.49 || half > 0.51 {
		t.Fatalf("utilisation = %v at half the ceiling, want ~0.5", half)
	}

	// ★ There is exactly one read budget to ask for. If a class had its own,
	// Budget would need a class as well as a kind — it does not, and cannot,
	// without becoming the third budget kind ADR-015 refused.
	if c.Budget(KindRead) == nil {
		t.Fatal("there is no read budget")
	}
	if got := c.Budget(KindRead).Ceiling.BitsPerSecond; got != ceiling {
		t.Errorf("the read ceiling changed to %v; there is one, shared", got)
	}

	// And the class changes nothing about accounting: withdrawing does not alter
	// the observed utilisation, whichever class is asked about.
	if state, _ := at(t, c, 0.99); state != StateWithdrawn {
		t.Fatalf("state = %v, want withdrawn", state)
	}
	before := c.Budget(KindRead).Utilisation()
	for _, class := range Classes() {
		c.Admits(KindRead, class)
	}
	if after := c.Budget(KindRead).Utilisation(); after != before {
		t.Errorf("asking what is admitted changed utilisation from %v to %v; the class orders "+
			"what is given up and accounts for nothing", before, after)
	}

	// The write budget is still separate, and a read storm does not move it.
	if got := c.Budget(KindWrite).Utilisation(); got != 0 {
		t.Errorf("read load moved write utilisation to %v", got)
	}
}
