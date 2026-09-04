// Package addr implements sdev1's address model: how an entity identifier
// becomes a key, and how that key names one leaf of the address trie.
//
// # The model
//
// An entity identifier is hashed to a 32-byte key. Descending the trie consumes
// one byte of that key per level, most significant first, and each byte selects
// one of [FanOut] children. A descent of depth d therefore names one of 256^d
// leaves and consumes d of the 32 available bytes.
//
// # Fan-out is constant; depth is what scales
//
// [FanOut] is 256 and is a compile-time constant. It is deliberately not
// configurable, and that is the invariant this package exists to protect: a
// descent is a byte walk only while the fan-out is exactly one byte. Any other
// value makes the mapping from key bytes to levels depend on configuration that
// is not carried alongside the data, so changing it would silently reinterpret
// every key ever written rather than failing loudly.
//
// Scale comes from depth instead. At depth 1 there are 256 leaves; at depth 4
// there are more than four billion; the 32-byte key bounds the trie at
// [MaxDepth] levels. Raising the cluster's live depth needs no change here,
// because [Descend] is a pure function of a key and a depth and holds no cluster
// state of its own.
//
// # Why a leaf identifier carries its own depth
//
// A [LeafID] records the depth that produced it as well as the prefix bytes.
// This is what makes a depth change a subdivision rather than a rename: a leaf
// recorded at depth 1 stays interpretable after the cluster moves to depth 2,
// and the deeper leaf is contained by the shallower one rather than replacing
// it. Without the depth, a bare prefix would be ambiguous — the same bytes name
// different leaves at different depths — and every stored leaf identifier would
// have to be reinterpreted whenever the cluster grew.
//
// # Purity
//
// Nothing here performs I/O, reads a clock, consults configuration or allocates
// a map. Two clients computing the same key at the same depth reach the same
// leaf, in any process, forever, without coordinating. That property is what
// lets a client route to the server holding a key by computing rather than by
// asking, and every other package in this module depends on it.
//
// The decision this package implements is recorded in
// docs/adr/ADR-001-address-space.md.
package addr
