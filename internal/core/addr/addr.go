package addr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// FanOut is the number of children at every level of the address trie.
//
// It is a compile-time constant and may not be read from configuration. A
// descent consumes one byte per level, which is only meaningful while the
// fan-out is exactly one byte wide; any other value would silently reinterpret
// every key ever written. TestFanOutIsExactlyOneByte pins this.
const FanOut = 1 << 8

// MaxDepth is the deepest descent the key space admits, bounded by the number of
// bytes a key has. Depth beyond the point where a leaf holds less than one
// segment is waste, but that is a capacity judgement rather than a limit this
// package can enforce.
const MaxDepth = sha256.Size

// TenantBytes is the width of the tenant prefix, giving 65,536 tenants.
//
// It is a constant for the same reason [FanOut] is: a variable boundary would
// make the meaning of every stored key depend on configuration the data does not
// carry. Widening it is a format change with a re-ingest cost, and is a decision
// to take deliberately rather than a knob to turn.
const TenantBytes = 2

// ErrDepthOutOfRange reports a descent depth outside 1..[MaxDepth]. It is
// returned rather than clamped, because a silently truncated leaf identifier
// routes to the wrong server and looks like a correct answer.
var ErrDepthOutOfRange = errors.New("addr: depth out of range")

// ErrMalformedLeafID reports text that does not name a leaf.
var ErrMalformedLeafID = errors.New("addr: malformed leaf identifier")

// TenantID names a tenant. It occupies the leading bytes of every key that
// tenant owns.
type TenantID [TenantBytes]byte

// TenantFromUint returns the TenantID for a numeric identifier, big-endian so
// that ordering by number matches ordering by bytes.
func TenantFromUint(n uint16) TenantID {
	return TenantID{byte(n >> 8), byte(n)}
}

// String renders a tenant as hex.
func (t TenantID) String() string { return hex.EncodeToString(t[:]) }

// Key is the address a datom is stored under: a tenant prefix followed by the
// entity's digest.
type Key [sha256.Size]byte

// KeyOf returns the Key for an entity within a tenant.
//
// The tenant is written LITERALLY into the leading bytes and is never hashed.
// That is the property everything else rests on: a tenant therefore occupies one
// contiguous subtree, so deleting a tenant is a subtree drop, pinning one to a
// region is a placement rule, and per-tenant policy has somewhere to attach.
// Hashing the tenant would balance load beautifully and destroy all three.
//
// The tenant is carved OUT of the digest rather than added to it, so a key stays
// 32 bytes and nothing downstream changes width. The remaining 30 bytes carry
// 240 bits of entity digest, far beyond any collision concern.
func KeyOf(tenant TenantID, entity string) Key {
	digest := sha256.Sum256([]byte(entity))
	var k Key
	copy(k[:TenantBytes], tenant[:])
	copy(k[TenantBytes:], digest[:len(k)-TenantBytes])
	return k
}

// TenantOf returns the tenant a key belongs to. It is a prefix read, not a
// computation.
func TenantOf(k Key) TenantID {
	var t TenantID
	copy(t[:], k[:TenantBytes])
	return t
}

// TenantSubtree returns the leaf identifier naming the whole subtree a tenant
// owns.
//
// Every key of that tenant descends through this leaf at any depth of
// [TenantBytes] or more, so a tenant-scoped operation is a prefix range rather
// than a scan. Below that depth tenants share leaves, which is legal and
// appropriate for a small deployment.
func (t TenantID) TenantSubtree() LeafID {
	var leaf LeafID
	leaf.Depth = TenantBytes
	copy(leaf.Prefix[:TenantBytes], t[:])
	return leaf
}

// LeafID names one leaf of the address trie.
//
// It carries the Depth that produced it as well as the prefix bytes, so that a
// leaf recorded under one cluster depth stays interpretable under another. Only
// the first Depth bytes of Prefix are meaningful; the remainder is zero.
type LeafID struct {
	Prefix [sha256.Size]byte
	Depth  uint8
}

// Bytes returns the meaningful prefix — the first Depth bytes.
func (l LeafID) Bytes() []byte {
	return l.Prefix[:l.Depth]
}

// Contains reports whether other lies within l's subtree.
//
// A leaf contains itself. A shallower leaf contains every deeper leaf sharing
// its prefix, which is what makes raising the cluster's depth a subdivision
// rather than a rename — and what makes a tenant's subtree contain every leaf of
// that tenant.
func (l LeafID) Contains(other LeafID) bool {
	if other.Depth < l.Depth {
		return false
	}
	return bytes.Equal(other.Prefix[:l.Depth], l.Prefix[:l.Depth])
}

// String returns the canonical text form, "<depth>:<hex prefix>".
//
// The depth is part of the identity rather than decoration: the same prefix
// bytes name different leaves at different depths, so a bare hex string would be
// ambiguous.
func (l LeafID) String() string {
	return strconv.Itoa(int(l.Depth)) + ":" + hex.EncodeToString(l.Bytes())
}

// ParseLeafID parses the canonical text form produced by [LeafID.String].
func ParseLeafID(s string) (LeafID, error) {
	depthText, prefixText, found := strings.Cut(s, ":")
	if !found {
		return LeafID{}, fmt.Errorf("%w: %q has no depth separator", ErrMalformedLeafID, s)
	}
	depth, err := strconv.Atoi(depthText)
	if err != nil {
		return LeafID{}, fmt.Errorf("%w: depth %q: %v", ErrMalformedLeafID, depthText, err)
	}
	if depth < 1 || depth > MaxDepth {
		return LeafID{}, fmt.Errorf("%w: depth %d not in 1..%d", ErrDepthOutOfRange, depth, MaxDepth)
	}
	raw, err := hex.DecodeString(prefixText)
	if err != nil {
		return LeafID{}, fmt.Errorf("%w: prefix %q: %v", ErrMalformedLeafID, prefixText, err)
	}
	if len(raw) != depth {
		return LeafID{}, fmt.Errorf("%w: depth %d declares %d prefix bytes, got %d",
			ErrMalformedLeafID, depth, depth, len(raw))
	}
	var leaf LeafID
	leaf.Depth = uint8(depth)
	copy(leaf.Prefix[:], raw)
	return leaf, nil
}

// Descend walks the address trie, consuming one byte of k per level, and returns
// the leaf reached at the given depth.
//
// It is a pure function of its arguments: no I/O, no clock, no configuration and
// no allocation of a map. The same key and depth reach the same leaf in every
// process, which is what lets two clients agree on placement without
// coordinating.
func Descend(k Key, depth uint8) (LeafID, error) {
	if depth < 1 || depth > MaxDepth {
		return LeafID{}, fmt.Errorf("%w: depth %d not in 1..%d", ErrDepthOutOfRange, depth, MaxDepth)
	}
	var leaf LeafID
	leaf.Depth = depth
	copy(leaf.Prefix[:depth], k[:depth])
	return leaf, nil
}
