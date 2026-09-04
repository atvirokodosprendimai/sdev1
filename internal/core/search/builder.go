package search

import (
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// TermsOf returns the terms one datom contributes to the index, and none for a
// datom that must not be indexed.
//
// ★ It is the ONE place that rule lives. The write path indexes as facts are
// asserted and [Builder] indexes by walking the log; if those two disagreed about
// which datoms produce terms, an index rebuilt from the log would answer
// differently from the one it replaced — and "the index is rebuildable" is the
// sentence that makes losing it a performance event rather than a data-loss one.
//
// ⚠ A RETRACTION contributes nothing: the fact it withdraws must stop being
// findable. ⚠ A REFERENCE contributes nothing either — its bytes are an entity
// name, not prose, and matching them as text would answer "what links to this"
// with something that only looks like an answer.
func TermsOf(d ports.Datom) []Term {
	if !d.Assert || d.IsReference {
		return nil
	}
	return Analyze(string(d.Value))
}

// Builder feeds an index by consuming the log, as ADR-010's subscription
// delivers it.
//
// It is a [subscribe.Sink]. Using the same primitive as streaming backup and the
// console means the index follows the log the one way everything else does,
// rather than being a third mechanism that can fall behind differently.
type Builder struct {
	name  string
	keys  crypt.Keystore
	index *Index

	// high is the last transaction this builder has indexed.
	//
	// ⚠ Delivery is AT LEAST ONCE — a sink that crashes after consuming and
	// before acknowledging sees the same entries again, and must tolerate it.
	// An index that simply appended would then hold each posting twice, which
	// changes what terms score without changing what they mean: the results stay
	// plausible and the ranking is wrong. A high-water mark is the cheapest
	// idempotence for a stream that arrives in order, which this one does.
	high tx.TxID
	seen bool
}

// NewBuilder returns a builder that feeds ix, sealing postings under ks.
func NewBuilder(name string, ks crypt.Keystore, ix *Index) *Builder {
	return &Builder{name: name, keys: ks, index: ix}
}

// Name identifies the builder to a purge.
func (b *Builder) Name() string { return b.name }

// Consume indexes the datoms of each entry and returns how many entries it
// accepted, counting from the first.
//
// ⚠ An entry is indexed ATOMICALLY or not at all. Sealing can fail partway
// through an entry, and postings already added for it would be added again on
// redelivery — the duplicate this type's high-water mark exists to prevent,
// arriving through the failure path instead.
func (b *Builder) Consume(entries []tail.Entry) int {
	for i, e := range entries {
		if b.seen && e.TxID.Compare(b.high) <= 0 {
			// Already indexed. Counted as accepted, because it is: refusing it
			// would stall the cursor on an entry that will never be new.
			continue
		}

		pending, err := b.postingsFor(e)
		if err != nil {
			return i
		}
		for _, p := range pending {
			b.index.Add(p.term, p.sealed)
		}
		b.high, b.seen = e.TxID, true
	}
	return len(entries)
}

// Highest is the last transaction the builder has indexed, and whether it has
// indexed anything.
func (b *Builder) Highest() (tx.TxID, bool) { return b.high, b.seen }

// sealedTerm pairs a term with the posting sealed under its subject's key.
type sealedTerm struct {
	term   Term
	sealed Sealed
}

func (b *Builder) postingsFor(e tail.Entry) ([]sealedTerm, error) {
	var out []sealedTerm
	for _, d := range e.Datoms {
		for _, term := range TermsOf(d) {
			sealed, err := Seal(b.keys, Posting{Subject: d.Entity, Term: term, From: d.TxID})
			if err != nil {
				return nil, fmt.Errorf("search: indexing %q: %w", d.Entity, err)
			}
			out = append(out, sealedTerm{term: term, sealed: sealed})
		}
	}
	return out, nil
}
