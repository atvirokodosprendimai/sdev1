package search

import (
	"context"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
)

// Confirm drops candidates whose datoms no longer carry any of the terms
// searched for.
//
// ⚠ THIS IS THE RULE THAT DECAYS QUIETEST. Skipping it makes every search faster
// and the damage shows only on data that changed since the index last saw it —
// so the tests that would notice are the ones nobody writes by accident.
//
// The index is DERIVED and always slightly behind the log, which is what makes a
// hit a candidate rather than an answer. Returning candidates unconfirmed makes
// the index the authority, which is exactly what ADR-021 refuses: a fact that was
// retracted a moment ago would still be returned, indistinguishably from one that
// was not.
//
// ★ A candidate is kept if ANY searched term still matches, because that is what
// made it a candidate. ⚠ Its SCORE is not recomputed: ranking against a real
// corpus is deferred (`BACKLOG.md` §27), and a stale score is a stale ORDER
// rather than a wrong membership. That limit is stated rather than hidden.
func Confirm(ctx context.Context, r ports.Reader, at ports.Snapshot, terms []Term, hits []Scored) ([]Scored, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	wanted := make(map[Term]bool, len(terms))
	for _, t := range terms {
		wanted[t] = true
	}

	out := make([]Scored, 0, len(hits))
	for _, h := range hits {
		matches, err := stillMatches(ctx, r, at, wanted, h.Posting.Subject)
		if err != nil {
			return nil, err
		}
		if matches {
			out = append(out, h)
		}
	}
	return out, nil
}

// stillMatches re-reads a subject's datoms and reports whether any wanted term is
// still present in them.
//
// ⚠ It goes through [TermsOf], the same rule the index is built with. Analysing
// the values differently here would make confirmation reject things the index was
// right to return, which reads as data loss rather than as a mismatch.
func stillMatches(ctx context.Context, r ports.Reader, at ports.Snapshot, wanted map[Term]bool, subject string) (bool, error) {
	datoms, err := r.Load(ctx, subject, at)
	if err != nil {
		return false, fmt.Errorf("search: confirming %q: %w", subject, err)
	}
	// ⚠ Reduced to what the entity CARRIES first. `Load` returns the visible
	// datoms including retractions, so scanning them raw would find the earlier
	// ASSERTION of an attribute that has since been retracted — and confirm a
	// fact that was withdrawn, which is precisely what this function exists to
	// prevent. Found by a mutant: swapping TermsOf for a raw analysis survived,
	// because no test yet held a retracted datom.
	for _, d := range ports.Carried(datoms) {
		for _, term := range TermsOf(d) {
			if wanted[term] {
				return true, nil
			}
		}
	}
	return false, nil
}
