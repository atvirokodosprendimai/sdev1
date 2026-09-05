package admit

import (
	"errors"
	"reflect"
	"testing"
)

func mustCeiling(t *testing.T, bps, withdraw, rejoin float64) Ceiling {
	t.Helper()
	c, err := NewCeiling(bps, withdraw, rejoin)
	if err != nil {
		t.Fatalf("NewCeiling(%v, %v, %v): %v", bps, withdraw, rejoin, err)
	}
	return c
}

func mustController(t *testing.T) *Controller {
	t.Helper()
	// A gigabit link, shedding reads at 90% and rejoining at 70%.
	c, err := NewController(
		mustCeiling(t, 1_000_000_000, 0.90, 0.70),
		mustCeiling(t, 1_000_000_000, 0.90, 0.70),
	)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

// TestReadSheddingNeverStopsWrites is the falsifier ADR-015 names in its
// Enforced-by header.
//
// A leaf has one writer, so a shed write is an outage rather than a re-route.
// Under a shared budget a read storm would stall that leaf's ingest, which turns
// a load spike into data not being accepted — the failure that actually matters.
func TestReadSheddingNeverStopsWrites(t *testing.T) {
	c := mustController(t)

	// A modest, healthy write rate.
	if err := c.Observe(KindWrite, 100_000_000); err != nil {
		t.Fatalf("Observe(write): %v", err)
	}
	writeBefore := c.Budget(KindWrite).Utilisation()

	// Reads go far past the ceiling — five times the whole link.
	if err := c.Observe(KindRead, 5_000_000_000); err != nil {
		t.Fatalf("Observe(read): %v", err)
	}
	if _, changed := c.Decide(); !changed || c.State() != StateWithdrawn {
		t.Fatalf("at %.1f× the ceiling the node is %v; it must withdraw",
			c.Budget(KindRead).Utilisation(), c.State())
	}

	// The write budget did not move, and writes still admit.
	if got := c.Budget(KindWrite).Utilisation(); got != writeBefore {
		t.Errorf("read load changed write utilisation from %v to %v — the budgets share state, "+
			"and a read storm can therefore stall a leaf's ingest", writeBefore, got)
	}
	if !c.Admits(KindWrite, ClassUser) {
		t.Fatal("writes stopped admitting because reads saturated the node; a shed write has " +
			"nowhere to go, so this is an outage rather than a re-route")
	}
	if c.Admits(KindRead, ClassUser) {
		t.Error("user reads still admit although the node withdrew")
	}

	// Even with the write budget itself over its own ceiling, writes admit —
	// because refusing one for BUSYNESS is not a thing this package does.
	if err := c.Observe(KindWrite, 9_000_000_000); err != nil {
		t.Fatalf("Observe(write): %v", err)
	}
	if _, _ = c.Decide(); !c.Admits(KindWrite, ClassUser) {
		t.Error("a write was refused for load; refusing a write is a durability decision made " +
			"elsewhere, and conflating the two makes a busy node look unsafe")
	}
}

// TestBudgetsShareNoState checks the separation structurally as well as
// behaviourally.
//
// ⚠ Checking only that two numbers differ would pass for two views over one
// counter. This asserts they are distinct values, and that moving one leaves the
// other's utilisation exactly where it was.
func TestBudgetsShareNoState(t *testing.T) {
	c := mustController(t)

	read := c.Budget(KindRead)
	write := c.Budget(KindWrite)
	if read == write {
		t.Fatal("the two budgets are the same value")
	}
	if read.Kind == write.Kind {
		t.Fatalf("both budgets report kind %v", read.Kind)
	}

	// Moving one leaves the other exactly where it was, in both directions.
	for _, c2 := range []struct {
		move, other Kind
	}{{KindRead, KindWrite}, {KindWrite, KindRead}} {
		ctrl := mustController(t)
		if err := ctrl.Observe(c2.other, 123_456_789); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		before := ctrl.Budget(c2.other).Utilisation()

		for _, rate := range []float64{0, 1e6, 1e9, 9e9} {
			if err := ctrl.Observe(c2.move, rate); err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if got := ctrl.Budget(c2.other).Utilisation(); got != before {
				t.Fatalf("moving %v to %v changed %v's utilisation from %v to %v",
					c2.move, rate, c2.other, before, got)
			}
		}
	}

	// There is no third kind to put a shared budget in.
	if got := len(Kinds()); got != 2 {
		t.Errorf("there are %d budget kinds, want exactly 2 — a third, especially a 'total', "+
			"is a shared budget wearing another name", got)
	}
	if c.Budget(KindUnset) != nil {
		t.Error("the unset kind resolves to a budget")
	}

	// And the Controller holds them as separate fields rather than one map.
	typ := reflect.TypeOf(Controller{})
	var budgetFields int
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type == reflect.TypeOf(Budget{}) {
			budgetFields++
		}
	}
	if budgetFields != 2 {
		t.Errorf("Controller holds %d Budget fields, want 2", budgetFields)
	}
}

// TestInvertedThresholdsAreRefused checks a node that would oscillate cannot be
// constructed.
//
// ⚠ EQUAL is covered, and equal is what people write. It flaps by construction:
// the node rejoins at exactly the load that made it leave.
func TestInvertedThresholdsAreRefused(t *testing.T) {
	for _, c := range []struct{ withdraw, rejoin float64 }{
		{0.9, 0.9},  // equal — the oscillating case
		{0.9, 0.95}, // inverted
		{0.5, 0.5},
		{0.5, 0.6},
	} {
		_, err := NewCeiling(1e9, c.withdraw, c.rejoin)
		if !errors.Is(err, ErrThresholdsInverted) {
			t.Errorf("withdraw %v rejoin %v: error = %v, want ErrThresholdsInverted",
				c.withdraw, c.rejoin, err)
		}
	}

	// A real gap is accepted.
	if _, err := NewCeiling(1e9, 0.9, 0.7); err != nil {
		t.Errorf("a ceiling with a proper gap was refused: %v", err)
	}
	// Rejoining at zero is allowed: it means "only rejoin when idle".
	if _, err := NewCeiling(1e9, 0.9, 0); err != nil {
		t.Errorf("a rejoin threshold of zero was refused: %v", err)
	}
}

// TestCeilingMustBeDeclared checks there is no default to fall back on.
func TestCeilingMustBeDeclared(t *testing.T) {
	for _, bps := range []float64{0, -1, -1e9} {
		if _, err := NewCeiling(bps, 0.9, 0.7); !errors.Is(err, ErrNoCeiling) {
			t.Errorf("%v bits per second: error = %v, want ErrNoCeiling — a node that measures "+
				"its own ceiling discovers it by exceeding it", bps, err)
		}
	}
	// A withdraw fraction outside (0, 1] is meaningless.
	for _, w := range []float64{0, -0.1, 1.5} {
		if _, err := NewCeiling(1e9, w, 0); !errors.Is(err, ErrNoCeiling) {
			t.Errorf("withdraw fraction %v: error = %v, want ErrNoCeiling", w, err)
		}
	}

	// A controller needs a ceiling for BOTH kinds.
	good := mustCeiling(t, 1e9, 0.9, 0.7)
	if _, err := NewController(good, Ceiling{}); !errors.Is(err, ErrNoCeiling) {
		t.Errorf("a controller with no write ceiling: error = %v, want ErrNoCeiling", err)
	}
	if _, err := NewController(Ceiling{}, good); !errors.Is(err, ErrNoCeiling) {
		t.Errorf("a controller with no read ceiling: error = %v, want ErrNoCeiling", err)
	}
}

// TestUtilisationIsAFractionOfTheCeiling checks one threshold means the same
// thing on links of different sizes.
func TestUtilisationIsAFractionOfTheCeiling(t *testing.T) {
	// A gigabit node and a ten-gigabit node, each at 90% of its own link.
	for _, c := range []struct {
		link, rate float64
	}{
		{1e9, 0.9e9},
		{10e9, 9e9},
		{100e6, 90e6},
	} {
		ctrl, err := NewController(
			mustCeiling(t, c.link, 0.90, 0.70),
			mustCeiling(t, c.link, 0.90, 0.70),
		)
		if err != nil {
			t.Fatalf("NewController: %v", err)
		}
		if err := ctrl.Observe(KindRead, c.rate); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		got := ctrl.Budget(KindRead).Utilisation()
		if diff := got - 0.9; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("a %v link at %v reports utilisation %v, want 0.9 — a fraction is what "+
				"makes one threshold meaningful across different links", c.link, c.rate, got)
		}
		if ctrl.Budget(KindRead).Rate() != c.rate {
			t.Errorf("the observed rate was not recorded")
		}
	}

	// A fresh budget is idle rather than undefined.
	fresh := mustController(t)
	if got := fresh.Budget(KindRead).Utilisation(); got != 0 {
		t.Errorf("an unobserved budget reports utilisation %v, want 0", got)
	}
}
