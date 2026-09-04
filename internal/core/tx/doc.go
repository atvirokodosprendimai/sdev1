// Package tx defines transaction identity: the value that answers "when did
// this happen" for the whole cluster rather than for one leaf.
//
// # Why a counter is not enough
//
// The obvious design is a monotonic counter per leaf. It is simpler, needs no
// clock, and is correct within its own leaf — and it makes a query spanning
// leaves undefined rather than merely limited. There is no relation ordering a
// counter in one leaf against a counter in another, so a time-travel query
// covering both returns some result, ordered by nothing, and looks fine. That
// failure appears only once data exists, and the identifier is written into
// every datom, so it cannot be corrected afterwards without rewriting all of
// them.
//
// # The order is TOTAL, and the tie-breakers are why
//
// A [TxID] is a hybrid logical clock reading, the leaf that minted it, and a
// per-leaf sequence. The clock alone is nearly enough: it never goes backwards
// and it carries causality between nodes. What it does not give is a decision
// when two leaves independently mint at the identical reading, which is
// ordinary rather than rare on a busy cluster. The leaf and the sequence break
// that tie, so no two distinct identifiers ever compare equal.
//
// A total order is exactly what an "as of" query spanning leaves needs in order
// to be a well-defined question at all.
//
// # How it fails, and how it recovers
//
// The sequence is safe because one leaf has one writer. If that ever stops
// being true — two processes minting for the same leaf — two identifiers can
// collide, and nothing here detects it: both look valid and the log records two
// events at one position. Recovery would mean re-minting, which is a re-ingest.
// The guard is upstream, in the single-writer property, not in this package.
//
// A restarted leaf does not carry its sequence in local state; it takes its
// position from the replicated log, so a crash loses nothing and a restart
// cannot reuse a position.
//
// Clock skew propagates through the clock rather than through this type; see
// package hlc for what that costs and why it is a cluster policy.
//
// # Encoding
//
// [TxID.Encode] is fixed-width and byte-comparable: comparing two encodings as
// bytes yields the same order as [TxID.Compare]. A segment index can therefore
// sort on the bytes and stride over them without decoding, which is the whole
// reason the identifier is a fixed-size value rather than something that grows
// with the number of participants.
//
// The decision this package implements is recorded in
// docs/adr/ADR-002-transaction-identity.md.
package tx
