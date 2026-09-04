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

// ErrDepthOutOfRange reports a descent depth outside 1..[MaxDepth]. It is
// returned rather than clamped, because a silently truncated leaf identifier
// routes to the wrong server and looks like a correct answer.
var ErrDepthOutOfRange = errors.New("addr: depth out of range")

// ErrMalformedLeafID reports text that does not name a leaf.
var ErrMalformedLeafID = errors.New("addr: malformed leaf identifier")

// Key is the digest an entity identifier hashes to, and the material a descent
// consumes one byte at a time.
type Key [sha256.Size]byte

// KeyOf returns the Key for an entity identifier.
//
// The entity is the unit of locality: everything about one entity resolves to
// one leaf, which is what makes a per-entity transaction single-leaf and so
// removes the need for a distributed commit.
func KeyOf(entity string) Key {
	return Key(sha256.Sum256([]byte(entity)))
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
// rather than a rename.
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
