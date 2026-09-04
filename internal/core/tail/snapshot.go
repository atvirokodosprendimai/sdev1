package tail

import (
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Snapshot is what a read is evaluated against: a published position, and the
// transaction point the reader asked to see.
//
// ★ The two are different questions and conflating them is the trap. The
// watermark says what has been PUBLISHED — a fact about the writer. The bound
// says what the reader asked to SEE — a fact about the query. A read that used
// only the watermark would return a different answer depending on when it
// happened to run, which is not a snapshot at all.
type Snapshot struct {
	// Watermark bounds the walk: entries above it are not published and are
	// unreachable.
	Watermark Watermark
	// Bound excludes entries the reader did not ask for, even when published.
	Bound tx.TxID
}

// Snapshot takes one, loading the watermark once.
//
// ⚠ There is no lock here, and there must never be one. This is a single
// acquire-load paired with a value the caller already had.
func (t *Tail) Snapshot(bound tx.TxID) Snapshot {
	return Snapshot{Watermark: t.Watermark(), Bound: bound}
}

// Read calls fn for each entry visible in the snapshot, in order, stopping early
// if fn returns false.
//
// Two independent limits apply, and a concurrent write fails BOTH: it is above
// the watermark because it was not published when the snapshot was taken, and
// above the bound because it was minted later. Either alone would be enough;
// having both is what makes a read repeatable regardless of how long it runs.
func (t *Tail) Read(s Snapshot, fn func(Entry) bool) {
	t.Walk(s.Watermark, func(e Entry) bool {
		if e.TxID.Compare(s.Bound) > 0 {
			// Published, but later than the reader asked for.
			return true
		}
		return fn(e)
	})
}
