package prefetch

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/erasure"
)

var (
	// ErrNoHedgeLeft reports that the reserve is spent.
	//
	// ★ A named error rather than a zero location, which a caller would fetch
	// from an empty node name and get a confusing failure somewhere else.
	ErrNoHedgeLeft = errors.New("prefetch: no reserve fragments remain to hedge with")

	// ErrBudgetTooSmall reports a budget that affords not even one block.
	//
	// ⚠ An empty window would be indistinguishable from "prefetching is off",
	// and a caller that meant to prefetch would never learn its budget was too
	// small for a single block.
	ErrBudgetTooSmall = errors.New("prefetch: the budget affords no whole block")
)

// Window is how far ahead a budget reaches.
type Window struct {
	// Blocks is how many consecutive blocks the budget covers.
	Blocks int
	// Bytes is what covering them will pull.
	Bytes int64
}

// PlanWindow says how many blocks a budget affords.
//
// ★ This is what makes "load the whole file into memory" a safe request. The
// answer is "as many blocks as your budget reaches": the whole file for a small
// one, a bounded prefix for a large one — the same request, two answers, neither
// of them an out-of-memory kill on a node serving other tenants.
func PlanWindow(h erasure.StripeHeader, blocksInBlob int, b Budget) (Window, error) {
	if err := h.Validate(); err != nil {
		return Window{}, err
	}
	if blocksInBlob < 0 {
		return Window{}, fmt.Errorf("prefetch: a blob cannot have %d blocks", blocksInBlob)
	}
	if b.Bytes <= 0 {
		return Window{}, fmt.Errorf("%w: no budget was declared", ErrBudgetTooSmall)
	}

	// One block costs k fragments — not k+m, because the hedge is not fetched.
	perBlock := int64(h.DataShards) * int64(h.FragmentSize)
	if perBlock <= 0 {
		return Window{}, fmt.Errorf("prefetch: a block of %d fragments at %d bytes has no size",
			h.DataShards, h.FragmentSize)
	}

	afford := int(b.Bytes / perBlock)
	if afford < 1 {
		return Window{}, fmt.Errorf("%w: a block costs %d bytes and the budget is %d",
			ErrBudgetTooSmall, perBlock, b.Bytes)
	}

	// The blob may be shorter than the budget reaches; take whichever binds.
	blocks := afford
	if blocksInBlob < blocks {
		blocks = blocksInBlob
	}
	return Window{Blocks: blocks, Bytes: int64(blocks) * perBlock}, nil
}

// Hedge returns the next reserve fragment to try, given how many have already
// been drawn.
//
// ⚠ The reserve is never part of the initial fetch. Drawing on it is what a
// caller does when a fetch is LATE, and fetching it upfront would be the m/k
// waste on every healthy read that this package exists to avoid.
func Hedge(p Plan, drawn int) (Location, error) {
	if drawn < 0 {
		return Location{}, fmt.Errorf("prefetch: %d hedges cannot have been drawn", drawn)
	}
	if drawn >= len(p.Hedge) {
		return Location{}, fmt.Errorf("%w: %d of %d already drawn",
			ErrNoHedgeLeft, drawn, len(p.Hedge))
	}
	// Nearest first, so a stalled fetch retries at the next best place rather
	// than an arbitrary one.
	return p.Hedge[drawn], nil
}
