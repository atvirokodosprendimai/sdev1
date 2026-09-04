package addr

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

// TestFanOutIsExactlyOneByte is the falsifier for ADR-001 rule 4. A fan-out that
// is not a whole byte stops the descent being a byte walk and silently changes
// what every stored key means, so the constant is pinned here rather than merely
// documented.
func TestFanOutIsExactlyOneByte(t *testing.T) {
	if FanOut != 1<<8 {
		t.Fatalf("FanOut = %d, want %d (one byte): a fan-out that is not a whole byte "+
			"reinterprets every key ever written", FanOut, 1<<8)
	}
	if MaxDepth != len(Key{}) {
		t.Fatalf("MaxDepth = %d, want %d: depth is bounded by the key's byte count",
			MaxDepth, len(Key{}))
	}
}

// TestDescendConsumesOneBytePerLevel checks the descent reads exactly the first
// d bytes of the key and no others.
func TestDescendConsumesOneBytePerLevel(t *testing.T) {
	k := KeyOf("some-entity")
	for d := 1; d <= MaxDepth; d++ {
		leaf, err := Descend(k, uint8(d))
		if err != nil {
			t.Fatalf("Descend(k, %d): unexpected error: %v", d, err)
		}
		if got, want := leaf.Bytes(), k[:d]; !bytes.Equal(got, want) {
			t.Errorf("Descend(k, %d).Bytes() = %x, want %x", d, got, want)
		}
		if leaf.Depth != uint8(d) {
			t.Errorf("Descend(k, %d).Depth = %d, want %d", d, leaf.Depth, d)
		}
	}
}

// TestLeafIDIsStableAcrossDepthChange is the falsifier ADR-001 names for its
// central claim: that scale comes from depth at constant fan-out. If raising the
// cluster's live depth renamed existing leaves, the design would be a resharding
// scheme and the 256-server ceiling would return as a migration cost.
//
// Two properties together are what "stable" means here. A descent is a pure
// function of (key, depth), so a leaf recorded at depth 1 still resolves to the
// same identifier after the cluster moves to depth 2; and the deeper leaf is
// CONTAINED BY the shallower one, so a depth increase subdivides rather than
// renames.
func TestLeafIDIsStableAcrossDepthChange(t *testing.T) {
	for _, entity := range []string{"a", "entity-two", "", "🌍", "a-much-longer-entity-identifier"} {
		k := KeyOf(entity)

		shallow, err := Descend(k, 1)
		if err != nil {
			t.Fatalf("Descend(%q, 1): %v", entity, err)
		}
		deeper, err := Descend(k, 2)
		if err != nil {
			t.Fatalf("Descend(%q, 2): %v", entity, err)
		}

		// The shallow leaf is unchanged by the existence of a deeper one.
		again, err := Descend(k, 1)
		if err != nil {
			t.Fatalf("Descend(%q, 1) second call: %v", entity, err)
		}
		if again != shallow {
			t.Errorf("%q: depth-1 leaf changed between calls: %v then %v", entity, shallow, again)
		}

		// Deepening subdivides: the deeper leaf lives inside the shallower one.
		if !shallow.Contains(deeper) {
			t.Errorf("%q: depth-2 leaf %v is not contained by depth-1 leaf %v — "+
				"a depth change renamed the leaf instead of subdividing it", entity, deeper, shallow)
		}
		// And containment is strict in the other direction.
		if deeper.Contains(shallow) {
			t.Errorf("%q: depth-1 leaf %v reported as contained by depth-2 leaf %v",
				entity, shallow, deeper)
		}
	}
}

// TestDescendIsDeterministic checks the same entity always reaches the same leaf,
// so two clients agree without coordinating. No clock, map iteration or
// randomness may enter the path.
func TestDescendIsDeterministic(t *testing.T) {
	const entity = "determinism-subject"
	first, err := Descend(KeyOf(entity), 3)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	for i := 0; i < 128; i++ {
		got, err := Descend(KeyOf(entity), 3)
		if err != nil {
			t.Fatalf("Descend iteration %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("iteration %d: Descend returned %v, want %v", i, got, first)
		}
	}
}

// TestKeyOfIsSHA256 pins the key derivation. ADR-001 rule 1 names SHA-256
// specifically, and the digest is what bounds the trie at MaxDepth levels.
func TestKeyOfIsSHA256(t *testing.T) {
	const entity = "digest-subject"
	want := sha256.Sum256([]byte(entity))
	if got := KeyOf(entity); got != Key(want) {
		t.Fatalf("KeyOf(%q) = %x, want %x", entity, got, want)
	}
}

// TestDescendRejectsOutOfRangeDepth checks a bad depth returns the sentinel
// rather than a silently truncated leaf.
func TestDescendRejectsOutOfRangeDepth(t *testing.T) {
	k := KeyOf("range-subject")
	for _, depth := range []uint8{0, MaxDepth + 1, 255} {
		if _, err := Descend(k, depth); !errors.Is(err, ErrDepthOutOfRange) {
			t.Errorf("Descend(k, %d) error = %v, want ErrDepthOutOfRange", depth, err)
		}
	}
}

// TestLeafIDStringRoundTrips checks a leaf identifier's text form carries the
// depth as well as the prefix. A prefix alone is ambiguous: the same bytes at
// depth 1 and depth 2 name different leaves.
func TestLeafIDStringRoundTrips(t *testing.T) {
	k := KeyOf("string-subject")
	for d := 1; d <= 4; d++ {
		leaf, err := Descend(k, uint8(d))
		if err != nil {
			t.Fatalf("Descend(k, %d): %v", d, err)
		}
		parsed, err := ParseLeafID(leaf.String())
		if err != nil {
			t.Fatalf("ParseLeafID(%q): %v", leaf.String(), err)
		}
		if parsed != leaf {
			t.Errorf("round trip at depth %d: got %v, want %v", d, parsed, leaf)
		}
	}
}
