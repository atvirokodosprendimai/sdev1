package search

import (
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
)

// ErrNoQuery reports a query that analyses to no terms.
//
// ★ Refused rather than treated as "match everything". An empty query matching
// the whole corpus is the most expensive thing a caller can ask for by accident.
var ErrNoQuery = errors.New("search: the query has no terms")

// Index maps a term to the sealed postings that mention it.
//
// ⚠ The dictionary holds TERMS, never subjects. A subject lives inside its
// sealed posting, which is what keeps erasure working: destroying the subject's
// key makes every one of its postings undecryptable without the index having to
// be found, visited or rewritten.
//
// ★ It is in memory. Persisting one needs the storage engine, but nothing about
// what a search MEANS needs it — which is why the meaning is proved here first.
type Index struct {
	postings map[Term][]Sealed
	// subjects counts distinct sealed postings, so document frequency has a
	// denominator that does not require walking the whole map.
	total int
}

// NewIndex returns an empty index.
func NewIndex() *Index { return &Index{postings: make(map[Term][]Sealed)} }

// Add records one sealed posting under a term.
//
// The term is supplied separately rather than read from the posting, because
// reading it would mean decrypting on the write path — and the indexer holds the
// plaintext already, at the moment it is analysing it.
func (ix *Index) Add(t Term, s Sealed) {
	ix.postings[t] = append(ix.postings[t], s)
	ix.total++
}

// Postings returns the sealed postings recorded under a term.
//
// They are still sealed: what a caller may actually see is [Visible]'s decision,
// not this one.
func (ix *Index) Postings(t Term) []Sealed { return ix.postings[t] }

// Terms reports how many distinct terms the index holds.
func (ix *Index) Terms() int { return len(ix.postings) }

// Query is one search.
type Query struct {
	// Text is the query as written. It is analysed with the same analyzer the
	// index used, or a term stored in one form is looked up in another and the
	// search silently returns nothing.
	Text string
	// Limit is how many results are wanted. Must be positive.
	Limit int
	// Facets are the attributes to break the matches down by, or nil.
	Facets []string
	// FacetBound is the largest matched set a facet may be computed over.
	// Ignored when Facets is empty.
	FacetBound int
	// Values supplies each subject's value per facet attribute, as an evaluator
	// read it. Missing means the subject has no value for that attribute, which
	// is not the same as having an empty one.
	Values map[string]map[string]string
}

// Scored is one candidate and what it scored.
type Scored struct {
	Posting Posting
	Score   float64
}

// Result is what a search answers.
//
// ⚠ Hits are CANDIDATES. The index is derived and always slightly behind the
// log, so a caller must confirm them against the datoms before treating them as
// true. That confirmation is not built yet, and until it is a result reflects
// what the index believes rather than what is.
type Result struct {
	Hits   []Scored
	Facets []FacetResult
}

// Search answers a query against the index.
//
// ⚠ Everything it returns has been through [Visible], so a shredded subject is
// absent from the hits AND from the facet counts. Faceting the candidates and
// filtering afterwards is the natural implementation and it leaks an erased
// subject as a number.
func (ix *Index) Search(ks crypt.Keystore, q Query) (Result, error) {
	if q.Limit <= 0 {
		return Result{}, fmt.Errorf("%w: got %d", ErrNoLimit, q.Limit)
	}
	terms := Analyze(q.Text)
	if len(terms) == 0 {
		return Result{}, fmt.Errorf("%w: %q", ErrNoQuery, q.Text)
	}

	hits := Rank(ks, ix, terms, q.Limit)

	result := Result{Hits: hits}
	if len(q.Facets) == 0 {
		return result, nil
	}

	// ⚠ Over the VISIBLE subjects only. See the doc comment.
	subjects := make([]string, 0, len(hits))
	for _, h := range hits {
		subjects = append(subjects, h.Posting.Subject)
	}
	for _, attribute := range q.Facets {
		f, err := Facet(attribute, subjects, q.Values[attribute], q.FacetBound)
		if err != nil {
			return Result{}, err
		}
		result.Facets = append(result.Facets, f)
	}
	return result, nil
}

// subjectsFor returns the distinct visible subjects a term matched, in a stable
// order.
//
// ⚠ The sort is not cosmetic. Everything downstream of it feeds a ranking, and a
// ranking whose input order comes from a map is non-deterministic per process —
// which passes every test that runs inside one binary.
func subjectsFor(ks crypt.Keystore, ix *Index, t Term) []Posting {
	visible := Visible(ks, ix.Postings(t))
	sort.Slice(visible, func(i, j int) bool {
		return visible[i].Subject < visible[j].Subject
	})
	return visible
}
