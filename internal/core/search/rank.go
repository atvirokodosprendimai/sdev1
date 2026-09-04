package search

import (
	"math"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
)

// Rank orders the subjects matching a query, best first, and takes at most
// limit of them.
//
// The score is term frequency weighted by inverse document frequency: a subject
// that mentions a term often scores higher, and a term that few subjects mention
// is worth more than one that many do. That is chosen for being explainable and
// deterministic rather than for being good — whether it ranks WELL needs a
// corpus, and that is deferred rather than claimed.
//
// ⚠ THE ORDER IS TOTAL, AND THAT IS THE PART THAT MATTERS. Ties are broken by
// subject, and every traversal that feeds the score is sorted first. A ranking
// that leaves ties to the order a Go map happened to yield returns a different
// answer per process — and every test that runs inside one binary passes anyway.
// Placement in this repository shipped exactly that defect.
//
// Only subjects a caller can actually see are ranked: everything reaches the
// score through [Visible], so a shredded subject is absent rather than
// zero-scored.
func Rank(ks crypt.Keystore, ix *Index, terms []Term, limit int) []Scored {
	if limit <= 0 {
		return nil
	}

	// A term appearing twice in a query must not count twice, and the deduped
	// set is walked in sorted order so the accumulation is reproducible.
	unique := make(map[Term]bool, len(terms))
	for _, t := range terms {
		unique[t] = true
	}
	ordered := make([]Term, 0, len(unique))
	for t := range unique {
		ordered = append(ordered, t)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	// best keeps one posting per subject — the first seen in the sorted walk —
	// so a result names a subject once however many terms it matched.
	scores := make(map[string]float64)
	best := make(map[string]Posting)

	corpus := float64(ix.total)
	for _, t := range ordered {
		matches := subjectsFor(ks, ix, t)
		if len(matches) == 0 {
			continue
		}

		// Inverse document frequency, damped so a term nothing else mentions
		// does not dominate outright. +1 keeps it positive for a term every
		// posting carries.
		idf := math.Log(1 + corpus/float64(len(matches)))

		frequency := make(map[string]int, len(matches))
		for _, m := range matches {
			frequency[m.Subject]++
			if _, seen := best[m.Subject]; !seen {
				best[m.Subject] = m
			}
		}
		for _, m := range matches {
			if _, counted := scores[m.Subject]; !counted {
				scores[m.Subject] = 0
			}
		}
		for subject, n := range frequency {
			scores[subject] += float64(n) * idf
		}
	}

	out := make([]Scored, 0, len(scores))
	for subject, score := range scores {
		out = append(out, Scored{Posting: best[subject], Score: score})
	}

	// ⚠ Score first, then SUBJECT. Without the second key the order among equal
	// scores is whatever the map yielded, which differs between processes.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Posting.Subject < out[j].Posting.Subject
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
