package addr

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

// tenant7 is an arbitrary tenant used where the value does not matter.
var tenant7 = TenantFromUint(7)

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

// TestKeyIsStillThirtyTwoBytes checks the tenant is carved OUT of the digest
// rather than added to it, so nothing downstream changes width.
func TestKeyIsStillThirtyTwoBytes(t *testing.T) {
	if got := len(KeyOf(tenant7, "anything")); got != sha256.Size {
		t.Fatalf("a key is %d bytes, want %d", got, sha256.Size)
	}
	if TenantBytes >= sha256.Size {
		t.Fatalf("TenantBytes = %d leaves no room for an entity digest", TenantBytes)
	}
}

// TestTenantIsNotHashed checks the tenant appears LITERALLY in the leading
// bytes. Every property ADR-016 claims rests on this: a hashed tenant is spread
// across the trie and owns no subtree at all.
func TestTenantIsNotHashed(t *testing.T) {
	for _, n := range []uint16{0, 1, 7, 255, 256, 65535} {
		tenant := TenantFromUint(n)
		k := KeyOf(tenant, "some-entity")
		if !bytes.Equal(k[:TenantBytes], tenant[:]) {
			t.Errorf("tenant %d: key begins %x, want the tenant's own bytes %x — "+
				"a hashed tenant owns no contiguous subtree", n, k[:TenantBytes], tenant[:])
		}
		if got := TenantOf(k); got != tenant {
			t.Errorf("TenantOf round trip: got %v, want %v", got, tenant)
		}
	}
}

// TestTenantOwnsAContiguousPrefix is the falsifier ADR-016 names in its
// Enforced-by header: every key of one tenant shares that tenant's leading
// bytes, whatever the entity.
func TestTenantOwnsAContiguousPrefix(t *testing.T) {
	tenant := TenantFromUint(4242)
	entities := []string{"a", "b", "a-much-longer-entity", "", "🌍", "zzzz"}

	for _, e := range entities {
		k := KeyOf(tenant, e)
		if !bytes.Equal(k[:TenantBytes], tenant[:]) {
			t.Fatalf("entity %q escaped its tenant's prefix: key begins %x, tenant is %x",
				e, k[:TenantBytes], tenant[:])
		}
	}

	// And entities still differ from one another inside the tenant, or the
	// prefix would have swallowed the entity.
	seen := make(map[Key]bool)
	for _, e := range entities {
		k := KeyOf(tenant, e)
		if seen[k] {
			t.Fatalf("two entities collided to one key inside tenant %v", tenant)
		}
		seen[k] = true
	}
}

// TestTenantSubtreeContainsEveryKeyOfThatTenant checks the subtree leaf contains
// the leaf of every key belonging to that tenant, at any depth from TenantBytes
// onward — which is what makes a tenant-scoped operation a prefix range.
func TestTenantSubtreeContainsEveryKeyOfThatTenant(t *testing.T) {
	tenant := TenantFromUint(99)
	subtree := tenant.TenantSubtree()
	if subtree.Depth != TenantBytes {
		t.Fatalf("subtree depth = %d, want %d", subtree.Depth, TenantBytes)
	}

	for _, e := range []string{"one", "two", "three", "four", "five"} {
		k := KeyOf(tenant, e)
		for d := uint8(TenantBytes); d <= 8; d++ {
			leaf, err := Descend(k, d)
			if err != nil {
				t.Fatalf("Descend(%q, %d): %v", e, d, err)
			}
			if !subtree.Contains(leaf) {
				t.Fatalf("entity %q at depth %d resolves to %v, outside its tenant's subtree %v",
					e, d, leaf, subtree)
			}
		}
	}
}

// TestDifferentTenantsNeverShareASubtree checks isolation is structural rather
// than conventional.
func TestDifferentTenantsNeverShareASubtree(t *testing.T) {
	a, b := TenantFromUint(1), TenantFromUint(2)
	sa, sb := a.TenantSubtree(), b.TenantSubtree()

	if sa.Contains(sb) || sb.Contains(sa) {
		t.Fatalf("tenant subtrees %v and %v are not disjoint", sa, sb)
	}
	for _, e := range []string{"x", "y", "z"} {
		ka, kb := KeyOf(a, e), KeyOf(b, e)
		la, err := Descend(ka, TenantBytes)
		if err != nil {
			t.Fatalf("Descend: %v", err)
		}
		lb, err := Descend(kb, TenantBytes)
		if err != nil {
			t.Fatalf("Descend: %v", err)
		}
		if la == lb {
			t.Fatalf("the same entity %q in two tenants reached one leaf %v", e, la)
		}
		if !sa.Contains(la) || !sb.Contains(lb) {
			t.Fatalf("entity %q did not land in its own tenant's subtree", e)
		}
	}
}

// TestDescendConsumesOneBytePerLevel checks the descent reads exactly the first
// d bytes of the key and no others.
func TestDescendConsumesOneBytePerLevel(t *testing.T) {
	k := KeyOf(tenant7, "some-entity")
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
func TestLeafIDIsStableAcrossDepthChange(t *testing.T) {
	for _, entity := range []string{"a", "entity-two", "", "🌍", "a-much-longer-entity-identifier"} {
		k := KeyOf(tenant7, entity)

		shallow, err := Descend(k, 1)
		if err != nil {
			t.Fatalf("Descend(%q, 1): %v", entity, err)
		}
		deeper, err := Descend(k, 2)
		if err != nil {
			t.Fatalf("Descend(%q, 2): %v", entity, err)
		}

		again, err := Descend(k, 1)
		if err != nil {
			t.Fatalf("Descend(%q, 1) second call: %v", entity, err)
		}
		if again != shallow {
			t.Errorf("%q: depth-1 leaf changed between calls: %v then %v", entity, shallow, again)
		}
		if !shallow.Contains(deeper) {
			t.Errorf("%q: depth-2 leaf %v is not contained by depth-1 leaf %v — "+
				"a depth change renamed the leaf instead of subdividing it", entity, deeper, shallow)
		}
		if deeper.Contains(shallow) {
			t.Errorf("%q: depth-1 leaf %v reported as contained by depth-2 leaf %v",
				entity, shallow, deeper)
		}
	}
}

// TestDescendIsDeterministic checks the same entity always reaches the same leaf,
// so two clients agree without coordinating.
func TestDescendIsDeterministic(t *testing.T) {
	const entity = "determinism-subject"
	first, err := Descend(KeyOf(tenant7, entity), 3)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	for i := 0; i < 128; i++ {
		got, err := Descend(KeyOf(tenant7, entity), 3)
		if err != nil {
			t.Fatalf("Descend iteration %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("iteration %d: Descend returned %v, want %v", i, got, first)
		}
	}
}

// TestKeyOfCarriesTheEntityDigest pins the entity half of the layout: the bytes
// after the tenant are the leading bytes of the entity's SHA-256 digest.
func TestKeyOfCarriesTheEntityDigest(t *testing.T) {
	const entity = "digest-subject"
	digest := sha256.Sum256([]byte(entity))
	k := KeyOf(tenant7, entity)

	if got, want := k[TenantBytes:], digest[:len(k)-TenantBytes]; !bytes.Equal(got, want) {
		t.Fatalf("key's entity half = %x, want the digest's leading bytes %x", got, want)
	}
}

// TestDescendRejectsOutOfRangeDepth checks a bad depth returns the sentinel
// rather than a silently truncated leaf.
func TestDescendRejectsOutOfRangeDepth(t *testing.T) {
	k := KeyOf(tenant7, "range-subject")
	for _, depth := range []uint8{0, MaxDepth + 1, 255} {
		if _, err := Descend(k, depth); !errors.Is(err, ErrDepthOutOfRange) {
			t.Errorf("Descend(k, %d) error = %v, want ErrDepthOutOfRange", depth, err)
		}
	}
}

// TestLeafIDStringRoundTrips checks a leaf identifier's text form carries the
// depth as well as the prefix.
func TestLeafIDStringRoundTrips(t *testing.T) {
	k := KeyOf(tenant7, "string-subject")
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
