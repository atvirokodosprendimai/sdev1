package prefetch

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/erasure"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

// fixture is one datacenter with three racks. srv-1 and srv-2 share rack-a, so
// they are nearer to each other than to srv-3 or srv-4.
func fixture(t *testing.T) topology.Map {
	t.Helper()
	const src = `{
	  "version":1,"depth":1,
	  "levels":["universe","planet","datacenter","rack","server"],
	  "root":{"level":"universe","name":"u","children":[
	    {"level":"planet","name":"earth","children":[
	      {"level":"datacenter","name":"dc-1","children":[
	        {"level":"rack","name":"rack-a","children":[
	          {"level":"server","name":"srv-1","weight":100},
	          {"level":"server","name":"srv-2","weight":100}]},
	        {"level":"rack","name":"rack-b","children":[
	          {"level":"server","name":"srv-3","weight":100},
	          {"level":"server","name":"srv-4","weight":100}]},
	        {"level":"rack","name":"rack-c","children":[
	          {"level":"server","name":"srv-5","weight":100},
	          {"level":"server","name":"srv-6","weight":100}]}]}]}]}
	}`
	m, err := topology.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("topology.Load: %v", err)
	}
	return m
}

func stripe(t *testing.T, k, m uint8, fragmentSize uint32) erasure.StripeHeader {
	t.Helper()
	h := erasure.StripeHeader{DataShards: k, ParityShards: m, FragmentSize: fragmentSize}
	if err := h.Validate(); err != nil {
		t.Fatalf("stripe RS(%d,%d): %v", k, m, err)
	}
	return h
}

// locationsFarToNear returns six locations whose nodes are ordered FURTHEST
// first, so an implementation that returned the input order fails the nearness
// test rather than passing by accident.
func locationsFarToNear() []Location {
	return []Location{
		{Index: 0, Node: "srv-5"},
		{Index: 1, Node: "srv-6"},
		{Index: 2, Node: "srv-3"},
		{Index: 3, Node: "srv-4"},
		{Index: 4, Node: "srv-2"},
		{Index: 5, Node: "srv-1"},
	}
}

// TestPlanFetchesExactlyKNotKPlusM is the falsifier ADR-018 names in its
// Enforced-by header.
//
// ⚠ It asserts BOTH halves: the fetch list is exactly k, AND the remainder is in
// the hedge rather than discarded. Checking only the first would pass for an
// implementation that threw the reserve away — which means one slow node stalls
// the block, the other failure this shape exists to avoid.
func TestPlanFetchesExactlyKNotKPlusM(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 4, 2, 1024)
	locs := locationsFarToNear()

	p, err := PlanFetch(h, locs, "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	if got := len(p.Fetch); got != 4 {
		t.Fatalf("the plan fetches %d fragments, want exactly k=4 — fetching k+m wastes m/k of "+
			"the link on every healthy read, and that is the same link admission control sheds "+
			"against", got)
	}
	if got := len(p.Hedge); got != 2 {
		t.Fatalf("the plan holds %d in reserve, want m=2 — discarding the reserve means one slow "+
			"node stalls the block", got)
	}

	// Together they account for every location, exactly once.
	seen := map[uint8]int{}
	for _, l := range append(append([]Location(nil), p.Fetch...), p.Hedge...) {
		seen[l.Index]++
	}
	if len(seen) != len(locs) {
		t.Errorf("fetch and hedge cover %d of %d fragments", len(seen), len(locs))
	}
	for idx, n := range seen {
		if n != 1 {
			t.Errorf("fragment %d appears %d times across fetch and hedge", idx, n)
		}
	}

	// The cost reported is k fragments, not k+m.
	if want := int64(4) * 1024; p.Bytes != want {
		t.Errorf("the plan reports %d bytes, want %d — the hedge costs nothing until drawn", p.Bytes, want)
	}
}

// TestPlanChoosesTheNearestK checks which k are chosen, not merely how many.
//
// ⚠ The input is ordered FURTHEST first, so an implementation that returned the
// input order fails. A fixture whose nearest node happened to be first would let
// that implementation pass.
func TestPlanChoosesTheNearestK(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 2, 4, 512)

	p, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	// srv-1 and srv-2 share rack-a, so they are the two nearest to srv-1.
	got := []string{p.Fetch[0].Node, p.Fetch[1].Node}
	near := map[string]bool{"srv-1": true, "srv-2": true}
	for i, n := range got {
		if !near[n] {
			t.Errorf("fetch[%d] is %q; the nearest two to srv-1 are its own rack-mates, and the "+
				"input was ordered furthest-first so returning it unchanged is not the answer", i, n)
		}
	}

	// The hedge is ordered by nearness too, so a hedge picks the next best node
	// rather than an arbitrary one.
	for i := 1; i < len(p.Hedge); i++ {
		prev, _ := m.Distance("srv-1", p.Hedge[i-1].Node)
		cur, _ := m.Distance("srv-1", p.Hedge[i].Node)
		if prev > cur {
			t.Errorf("the hedge is not nearest-first: %q (%d) before %q (%d)",
				p.Hedge[i-1].Node, prev, p.Hedge[i].Node, cur)
		}
	}

	// Viewed from elsewhere, a different k is chosen — so the ordering is about
	// the vantage point rather than a fixed preference.
	far, err := PlanFetch(h, locationsFarToNear(), "srv-5", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch from srv-5: %v", err)
	}
	fromFar := map[string]bool{"srv-5": true, "srv-6": true}
	for i, l := range far.Fetch {
		if !fromFar[l.Node] {
			t.Errorf("from srv-5, fetch[%d] is %q; the nearest are its own rack-mates", i, l.Node)
		}
	}
}

// TestOverBudgetPlanIsRefusedNotTruncated checks no partial plan is ever
// returned.
func TestOverBudgetPlanIsRefusedNotTruncated(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 4, 2, 1<<20) // 4 MiB per block

	p, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 1 << 20})
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("a 4 MiB plan against a 1 MiB budget: error = %v, want ErrOverBudget", err)
	}
	if len(p.Fetch) != 0 || len(p.Hedge) != 0 || p.Bytes != 0 {
		t.Errorf("a partial plan was returned alongside the refusal: %+v — fewer than k "+
			"fragments cannot reconstruct, so a truncated plan spends bandwidth and delivers "+
			"nothing", p)
	}

	// Exactly at the budget is allowed; the refusal is for EXCEEDING it.
	if _, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{Bytes: 4 << 20}); err != nil {
		t.Errorf("a plan costing exactly its budget was refused: %v", err)
	}

	// No budget at all is a refusal rather than an unlimited plan.
	if _, err := PlanFetch(h, locationsFarToNear(), "srv-1", m, Budget{}); !errors.Is(err, ErrOverBudget) {
		t.Errorf("an undeclared budget: error = %v, want ErrOverBudget", err)
	}
}

// TestTooFewFragmentsIsRefused checks a plan that could not succeed is not made.
func TestTooFewFragmentsIsRefused(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 4, 2, 1024)

	short := locationsFarToNear()[:3]
	p, err := PlanFetch(h, short, "srv-1", m, Budget{Bytes: 1 << 20})
	if !errors.Is(err, ErrTooFewFragments) {
		t.Fatalf("three locations for an RS(4,2) stripe: error = %v, want ErrTooFewFragments", err)
	}
	if len(p.Fetch) != 0 {
		t.Error("a plan was returned alongside the refusal")
	}

	// Exactly k is enough, and yields an empty hedge rather than an error.
	exact := locationsFarToNear()[:4]
	got, err := PlanFetch(h, exact, "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("exactly k locations: %v", err)
	}
	if len(got.Fetch) != 4 || len(got.Hedge) != 0 {
		t.Errorf("with exactly k locations the plan is %d fetch and %d hedge, want 4 and 0",
			len(got.Fetch), len(got.Hedge))
	}

	// An unusable scheme is refused by the stripe's own validation rather than
	// planned around.
	if _, err := PlanFetch(erasure.StripeHeader{}, exact, "srv-1", m, Budget{Bytes: 1 << 20}); err == nil {
		t.Error("a plan was made for a stripe with no valid scheme")
	}
}

// TestPlanIsAValueWithNoSideEffects checks ignoring a plan costs exactly
// nothing, which is what makes a prefetch a hint.
func TestPlanIsAValueWithNoSideEffects(t *testing.T) {
	m := fixture(t)
	h := stripe(t, 3, 3, 2048)
	locs := locationsFarToNear()

	before := append([]Location(nil), locs...)

	first, err := PlanFetch(h, locs, "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch: %v", err)
	}

	// The caller's slice is untouched.
	for i := range before {
		if locs[i] != before[i] {
			t.Fatalf("planning reordered the caller's slice at %d: %+v became %+v",
				i, before[i], locs[i])
		}
	}

	// Planning again gives the same answer — it is a pure function of its
	// inputs, so a caller that ignores one is where it started.
	for i := 0; i < 10; i++ {
		again, err := PlanFetch(h, locs, "srv-1", m, Budget{Bytes: 1 << 20})
		if err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
		if len(again.Fetch) != len(first.Fetch) || again.Bytes != first.Bytes {
			t.Fatalf("repeat %d differs in shape: %+v vs %+v", i, again, first)
		}
		for j := range first.Fetch {
			if again.Fetch[j] != first.Fetch[j] {
				t.Fatalf("repeat %d differs at fetch[%d]: %+v vs %+v",
					i, j, again.Fetch[j], first.Fetch[j])
			}
		}
	}

	// Mutating the returned plan does not affect a later one, so a caller cannot
	// corrupt the next read by holding onto this one.
	first.Fetch[0] = Location{Index: 99, Node: "nowhere"}
	fresh, err := PlanFetch(h, locs, "srv-1", m, Budget{Bytes: 1 << 20})
	if err != nil {
		t.Fatalf("PlanFetch after mutation: %v", err)
	}
	if fresh.Fetch[0].Node == "nowhere" {
		t.Error("mutating a returned plan changed a later one")
	}
}
