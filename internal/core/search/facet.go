package search

import (
	"errors"
	"fmt"
	"sort"
)

// ErrFacetTooWide reports a matched set larger than the facet's declared bound.
//
// ⚠ It is a REFUSAL, not a signal to estimate. An approximate count that is not
// labelled approximate is a lie, and a facet count is precisely the number
// somebody reconciles against a total. Refusing returns the caller to a narrower
// query, which works; an unlabelled estimate produces a number somebody acts on.
var ErrFacetTooWide = errors.New("search: the matched set is larger than the facet bound")

// ErrNoFacetBound reports a facet requested without a bound.
var ErrNoFacetBound = errors.New("search: a facet needs a positive bound")

// Count is one value and how many times it occurred.
type Count struct {
	Value string
	N     int
}

// FacetResult is the exact breakdown of one attribute over a matched set.
//
// ⚠ There is no "approximate" field and none may be added. A consumer that can
// ask whether a count is exact will eventually be given one that is not, and the
// whole point of this type is that the question never arises.
type FacetResult struct {
	// Attribute is what was counted.
	Attribute string
	// Counts are the values, most frequent first, ties broken by value so the
	// order is total and two callers see the same answer.
	Counts []Count
	// Total is the size of the matched set the counts were taken over.
	Total int
}

// Facet counts an attribute exactly over a matched set, or refuses.
//
// `values` is the attribute's value per matched subject, as the evaluator read
// it. A subject missing from the map is counted under no value at all rather
// than under an empty one — "has no class" and "has an empty class" are
// different facts, and merging them is the same conflation an unbound binding
// exists to prevent.
//
// ★ The bound is the caller's declared ceiling on how much work this may cost.
// It is checked BEFORE any counting, so an over-wide facet spends nothing.
func Facet(attribute string, subjects []string, values map[string]string, bound int) (FacetResult, error) {
	if bound <= 0 {
		return FacetResult{}, fmt.Errorf("%w: got %d", ErrNoFacetBound, bound)
	}
	if len(subjects) > bound {
		return FacetResult{}, fmt.Errorf("%w: %d matched, bound is %d", ErrFacetTooWide, len(subjects), bound)
	}

	tally := make(map[string]int, len(subjects))
	for _, s := range subjects {
		v, ok := values[s]
		if !ok {
			continue
		}
		tally[v]++
	}

	counts := make([]Count, 0, len(tally))
	for v, n := range tally {
		counts = append(counts, Count{Value: v, N: n})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].N != counts[j].N {
			return counts[i].N > counts[j].N
		}
		return counts[i].Value < counts[j].Value
	})

	return FacetResult{Attribute: attribute, Counts: counts, Total: len(subjects)}, nil
}
