package link

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// ErrDepthRequired reports a walk asked for without a bound.
//
// ★ Refused rather than defaulted to unbounded. A walk over a graph the caller
// does not control is a scan they did not ask for, and the depth is the only
// thing standing between them and one.
var ErrDepthRequired = errors.New("link: a walk needs a positive depth")

// ErrCycle reports a walk that returned to an entity it had already reached.
//
// ⚠ Reported rather than truncated. A partial path that stops quietly reads
// exactly like a complete one, and the caller has no way to tell.
//
// ⚠ Cycles here are not hypothetical. A hierarchy edited over time can contain a
// loop that exists only at instants BETWEEN two edits — visible in a historical
// query and in no current one.
var ErrCycle = errors.New("link: the walk returned to an entity it had already reached")

// Resolver returns the outbound references of one entity as they stood at a
// snapshot.
//
// ★ The snapshot is a PARAMETER, and that is deliberate: a caller structurally
// cannot resolve a hop without saying when. An interface whose method took only
// the entity would make "resolve at now" the easy path, and the easy path is how
// a traversal ends up assembling a tree that never existed.
type Resolver interface {
	// References returns the entities this entity points at, at this snapshot.
	// An entity that does not exist, was retracted, or was erased all return an
	// empty slice and no error — see [Walk].
	References(entity string, at temporal.Query) ([]string, error)
}

// Path is one entity reached by a walk, and how far from the root it was found.
type Path struct {
	Entity string
	Depth  int
}

// Walk returns everything reachable from root, within depth hops, as the graph
// stood at ONE instant.
//
// ⚠ THE SNAPSHOT IS TAKEN ONCE AND USED FOR EVERY HOP. That is this package's
// reason to exist. Resolving hop n+1 at a fresh instant assembles a tree out of
// parts that each existed and that as a whole never did — and it is invisible,
// because every node in it is real and nothing about the answer looks wrong.
//
// ⚠ An unresolvable reference is an ORDINARY ABSENCE. A target that was
// retracted, never existed, or was ERASED all look identical here, and must:
// distinguishing them would rebuild the existence oracle crypto-shredding exists
// to remove.
//
// The result is breadth-first from the root and does not include the root.
func Walk(r Resolver, root string, at temporal.Query, depth int) ([]Path, error) {
	if depth <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrDepthRequired, depth)
	}

	seen := map[string]bool{root: true}
	var out []Path

	frontier := []string{root}
	for level := 1; level <= depth && len(frontier) > 0; level++ {
		var next []string
		for _, entity := range frontier {
			// ⚠ `at` — the snapshot the CALLER gave — not a fresh one. There is
			// deliberately no other instant in scope to reach for.
			targets, err := r.References(entity, at)
			if err != nil {
				return nil, fmt.Errorf("link: resolve %q: %w", entity, err)
			}
			for _, target := range targets {
				if seen[target] {
					return nil, fmt.Errorf("%w: %q", ErrCycle, target)
				}
				seen[target] = true
				out = append(out, Path{Entity: target, Depth: level})
				next = append(next, target)
			}
		}
		frontier = next
	}
	return out, nil
}
