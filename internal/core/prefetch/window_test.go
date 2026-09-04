package prefetch

import (
	"errors"
	"testing"
)

// TestWindowIsBoundedByBudgetNotBlobSize checks the same request gives two
// answers.
//
// ⚠ Both cases are needed. A window on a SMALL blob proves nothing about the
// bound, because the blob is the limit rather than the budget — the case that
// matters is the large one, where an unbounded implementation would return the
// whole thing.
func TestWindowIsBoundedByBudgetNotBlobSize(t *testing.T) {
	h := stripe(t, 4, 2, 1<<20) // 4 MiB per block
	budget := Budget{Bytes: 40 << 20}

	// A small blob: the BLOB binds, and the whole thing is covered.
	small, err := PlanWindow(h, 3, budget)
	if err != nil {
		t.Fatalf("PlanWindow(small): %v", err)
	}
	if small.Blocks != 3 {
		t.Errorf("a 3-block blob under a 10-block budget covers %d blocks, want 3", small.Blocks)
	}

	// A large blob: the BUDGET binds, and it is emphatically not the whole
	// thing. This is the case that would be an out-of-memory kill.
	large, err := PlanWindow(h, 1_000_000, budget)
	if err != nil {
		t.Fatalf("PlanWindow(large): %v", err)
	}
	if large.Blocks != 10 {
		t.Fatalf("a million-block blob under a 40 MiB budget covers %d blocks, want 10 — "+
			"'load every part into memory' is right for a small blob and an out-of-memory kill "+
			"for a large one", large.Blocks)
	}
	if large.Bytes > budget.Bytes {
		t.Errorf("the window will pull %d bytes against a budget of %d", large.Bytes, budget.Bytes)
	}

	// The cost is k fragments per block, not k+m — the hedge is not fetched.
	if want := int64(10) * 4 * (1 << 20); large.Bytes != want {
		t.Errorf("the window reports %d bytes, want %d (k fragments per block)", large.Bytes, want)
	}

	// A larger budget reaches further, so the bound is the budget rather than a
	// fixed cap.
	further, err := PlanWindow(h, 1_000_000, Budget{Bytes: 80 << 20})
	if err != nil {
		t.Fatalf("PlanWindow(further): %v", err)
	}
	if further.Blocks <= large.Blocks {
		t.Errorf("doubling the budget covered %d blocks, not more than %d",
			further.Blocks, large.Blocks)
	}
}

// TestWindowOfZeroIsRefusedNotSilent checks a caller learns its budget is too
// small.
func TestWindowOfZeroIsRefusedNotSilent(t *testing.T) {
	h := stripe(t, 4, 2, 1<<20) // 4 MiB per block

	w, err := PlanWindow(h, 100, Budget{Bytes: 1 << 20})
	if !errors.Is(err, ErrBudgetTooSmall) {
		t.Fatalf("a budget affording no whole block: error = %v, want ErrBudgetTooSmall — an "+
			"empty window is indistinguishable from 'prefetching is off', and a caller that "+
			"meant to prefetch would never learn why nothing happened", err)
	}
	if w.Blocks != 0 || w.Bytes != 0 {
		t.Errorf("a window was returned alongside the refusal: %+v", w)
	}

	// No budget at all is the same refusal rather than an unlimited window.
	if _, err := PlanWindow(h, 100, Budget{}); !errors.Is(err, ErrBudgetTooSmall) {
		t.Errorf("an undeclared budget: error = %v, want ErrBudgetTooSmall", err)
	}

	// Exactly one block's worth affords exactly one block.
	one, err := PlanWindow(h, 100, Budget{Bytes: 4 << 20})
	if err != nil {
		t.Fatalf("a budget of exactly one block: %v", err)
	}
	if one.Blocks != 1 {
		t.Errorf("a one-block budget covers %d blocks, want 1", one.Blocks)
	}

	// A blob with no blocks yields a window of none, without an error — there is
	// nothing to read ahead into, which is different from a budget being wrong.
	empty, err := PlanWindow(h, 0, Budget{Bytes: 40 << 20})
	if err != nil {
		t.Errorf("a zero-block blob: %v, want no error", err)
	}
	if empty.Blocks != 0 {
		t.Errorf("a zero-block blob covers %d blocks, want 0", empty.Blocks)
	}
}

// TestHedgeIsDrawnOnlyWhenLate checks the reserve is never fetched upfront.
//
// ⚠ Asserting the hedge list is non-empty would pass for an implementation that
// fetched it. The observable form is that no hedge location appears in the FETCH
// list, which is what a caller would actually request.
func TestHedgeIsDrawnOnlyWhenLate(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 2, 4, 512)

	p, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	inFetch := map[uint8]bool{}
	for _, l := range p.Fetch {
		inFetch[l.Index] = true
	}
	for _, l := range p.Hedge {
		if inFetch[l.Index] {
			t.Errorf("fragment %d is in BOTH the fetch list and the hedge; the reserve must not "+
				"be fetched upfront, or every healthy read pays for m/k it does not need", l.Index)
		}
	}
	if len(p.Fetch) != 2 {
		t.Fatalf("the fetch list holds %d, want k=2", len(p.Fetch))
	}

	// A hedge is produced only when asked for.
	first, err := Hedge(p, 0)
	if err != nil {
		t.Fatalf("Hedge(0): %v", err)
	}
	if inFetch[first.Index] {
		t.Error("the first hedge is a fragment already being fetched")
	}
	if first.Node == "" {
		t.Error("the hedge names no node")
	}
}

// TestHedgePreservesNearestFirst checks successive hedges move outward.
func TestHedgePreservesNearestFirst(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 2, 4, 512)

	p, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	last := -1
	for i := 0; i < len(p.Hedge); i++ {
		got, err := Hedge(p, i)
		if err != nil {
			t.Fatalf("Hedge(%d): %v", i, err)
		}
		d, derr := m.Distance("srv-1", got.Node)
		if derr != nil {
			t.Fatalf("Distance to %q: %v", got.Node, derr)
		}
		if d < last {
			t.Errorf("hedge %d is at distance %d, nearer than the previous %d — a stalled fetch "+
				"must retry at the NEXT best place, not an arbitrary one", i, d, last)
		}
		last = d
		if got != p.Hedge[i] {
			t.Errorf("Hedge(%d) returned %+v, want the plan's own reserve entry %+v",
				i, got, p.Hedge[i])
		}
	}
}

// TestHedgeExhaustionIsNamed checks a spent reserve says so.
func TestHedgeExhaustionIsNamed(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 2, 4, 512)

	p, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	n := len(p.Hedge)
	got, err := Hedge(p, n)
	if !errors.Is(err, ErrNoHedgeLeft) {
		t.Fatalf("drawing hedge %d of %d: error = %v, want ErrNoHedgeLeft — a zero location "+
			"would be fetched from an empty node name and fail confusingly somewhere else", n, n, err)
	}
	if got != (Location{}) {
		t.Error("a location was returned alongside the exhaustion error")
	}

	// A negative draw count is refused rather than indexing backwards.
	if _, err := Hedge(p, -1); err == nil {
		t.Error("a negative draw count was accepted")
	}

	// A plan with no reserve at all is exhausted from the start.
	exact, err := PlanFetch(h, locationsFarToNear()[:2], "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch with exactly k: %v", err)
	}
	if _, err := Hedge(exact, 0); !errors.Is(err, ErrNoHedgeLeft) {
		t.Errorf("a plan with no reserve: error = %v, want ErrNoHedgeLeft", err)
	}
}
