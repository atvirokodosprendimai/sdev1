// Package addr implements sdev1's address model: how an entity identifier
// becomes a key, and how that key names one leaf of the address trie.
//
// # The model
//
// A key is 32 bytes: [TenantBytes] of tenant identifier, written literally,
// followed by the leading bytes of the entity's SHA-256 digest.
//
//	byte  0 1 | 2 3 4 5 ................................. 31
//	      tenant | entity digest (30 bytes, 240 bits)
//
// Descending the trie consumes one byte of that key per level, most significant
// first, and each byte selects one of [FanOut] children. A descent of depth d
// therefore names one of 256^d leaves and consumes d of the 32 available bytes.
//
// # The tenant is NOT hashed, and everything rests on that
//
// Because the tenant is written literally into the leading bytes, a tenant
// occupies one CONTIGUOUS SUBTREE. Deleting a tenant is a subtree drop; pinning
// one to a region is a placement rule; per-tenant durability, retention and
// compression have a subtree to attach to; and a busy tenant is absorbed by the
// same depth mechanism that scales everything else, because its subtree simply
// grows deeper.
//
// Hashing the tenant together with the entity would spread each tenant evenly
// across the trie — excellent for load balance, and it destroys every one of
// those properties. The tenant is carved OUT of the digest rather than added to
// it, so a key stays 32 bytes and nothing downstream changes width.
//
// Tenants share leaves below depth [TenantBytes]; isolation begins at that
// depth.
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
