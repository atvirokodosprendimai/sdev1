package prefetch

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/erasure"
	"github.com/atvirokodosprendimai/sdev1/internal/core/placement"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

var (
	// ErrOverBudget reports a plan that would exceed the declared bytes.
	//
	// ★ It returns NO plan. A truncated one holds fewer than k fragments and
	// cannot reconstruct, so it would spend bandwidth and deliver nothing —
	// strictly worse than not prefetching, while the caller's ordinary read path
	// always works.
	ErrOverBudget = errors.New("prefetch: the plan would exceed the declared budget")

	// ErrTooFewFragments reports fewer known locations than the stripe needs.
	ErrTooFewFragments = errors.New("prefetch: fewer known fragment locations than the stripe needs")
)

// Location is one fragment and the node holding it.
type Location struct {
	// Index is the fragment's position in the stripe.
	Index uint8
	// Node is where it is.
	Node string
}

// Budget is how many bytes one read may spend on prefetching.
//
// ⚠ It is declared by the caller, in bytes. "The whole file" is not a budget:
// the same instruction is correct for a small blob and an out-of-memory kill for
// a large one.
type Budget struct {
	Bytes int64
}

// Plan is what a read should ask for.
type Plan struct {
	// Fetch is exactly k locations, nearest first.
	Fetch []Location
	// Hedge is the remainder, nearest first. It is CARRIED, not fetched — drawn
	// on only when a fetch is late.
	Hedge []Location
	// Bytes is what Fetch will pull, so a caller sees the cost before paying it.
	Bytes int64
}

// PlanFetch chooses which fragments to ask for.
//
// ★ It takes k from the stripe's own header rather than from a parameter, so a
// caller cannot ask for a number of fragments the stripe does not need.
//
// ⚠ It returns exactly k in Fetch and the rest in Hedge. Returning k+m would
// waste m/k of the link on every healthy read; returning only k and discarding
// the rest would let one slow node stall the block.
func PlanFetch(h erasure.StripeHeader, locations []Location, from string, m topology.Map, b Budget) (Plan, error) {
	if err := h.Validate(); err != nil {
		return Plan{}, err
	}
	k := int(h.DataShards)
	if len(locations) < k {
		return Plan{}, fmt.Errorf("%w: %d location(s) known and RS(%d,%d) needs %d",
			ErrTooFewFragments, len(locations), h.DataShards, h.ParityShards, k)
	}

	ordered := byNearness(locations, from, m)

	// The cost is what Fetch pulls: k fragments, not k+m. The hedge costs
	// nothing until it is drawn on.
	bytes := int64(k) * int64(h.FragmentSize)
	if b.Bytes > 0 && bytes > b.Bytes {
		return Plan{}, fmt.Errorf("%w: %d fragment(s) of %d bytes is %d, and the budget is %d",
			ErrOverBudget, k, h.FragmentSize, bytes, b.Bytes)
	}
	if b.Bytes <= 0 {
		return Plan{}, fmt.Errorf("%w: no budget was declared", ErrOverBudget)
	}

	return Plan{
		Fetch: append([]Location(nil), ordered[:k]...),
		Hedge: append([]Location(nil), ordered[k:]...),
		Bytes: bytes,
	}, nil
}

// byNearness orders locations by the topology's own distance metric.
//
// ★ It reuses [placement.Nearest] rather than computing a second distance, so
// "near" means the same thing to a prefetch as it does to a client choosing a
// replica. A second metric here would eventually disagree with placement's, and
// the disagreement would be invisible.
func byNearness(locations []Location, from string, m topology.Map) []Location {
	nodes := make([]string, len(locations))
	for i, l := range locations {
		nodes[i] = l.Node
	}
	order := placement.Nearest(nodes, from, m)

	// Walk the ordered node names and take one unconsumed location per name, so
	// two fragments on one node keep their relative order.
	taken := make([]bool, len(locations))
	out := make([]Location, 0, len(locations))
	for _, name := range order {
		for i, l := range locations {
			if !taken[i] && l.Node == name {
				taken[i] = true
				out = append(out, l)
				break
			}
		}
	}
	// Anything Nearest did not name — it never drops members, so this is empty
	// in practice — is appended rather than lost.
	for i, l := range locations {
		if !taken[i] {
			out = append(out, l)
		}
	}
	return out
}
