package tx

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
)

func leaf(t *testing.T, entity string, depth uint8) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(1), entity), depth)
	if err != nil {
		t.Fatalf("Descend(%q, %d): %v", entity, depth, err)
	}
	return l
}

// TestCompareIsATotalOrder checks Compare is a strict total order over generated
// identifiers. "Total" is the property that matters: a partial order leaves two
// identifiers incomparable, and a cross-leaf AS OF over incomparable
// identifiers has no well-defined answer.
func TestCompareIsATotalOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))
	ids := make([]TxID, 0, 200)
	for i := 0; i < 200; i++ {
		ids = append(ids, TxID{
			HLC:  hlc.Timestamp{Wall: rng.Int63n(4), Logical: uint32(rng.Intn(3))},
			Leaf: leaf(t, string(rune('a'+rng.Intn(4))), 1),
			Seq:  uint32(rng.Intn(3)),
		})
	}

	for _, a := range ids {
		if a.Compare(a) != 0 {
			t.Fatalf("Compare is not reflexive for %v", a)
		}
		for _, b := range ids {
			ab, ba := a.Compare(b), b.Compare(a)
			if sign(ab) != -sign(ba) {
				t.Fatalf("Compare is not antisymmetric: %v vs %v gave %d and %d", a, b, ab, ba)
			}
			// Totality: two identifiers compare equal only when they ARE equal.
			if ab == 0 && a != b {
				t.Fatalf("Compare says %v and %v are equal but they are distinct — the order is not total", a, b)
			}
			for _, c := range ids {
				if a.Compare(b) < 0 && b.Compare(c) < 0 && a.Compare(c) >= 0 {
					t.Fatalf("Compare is not transitive: %v < %v < %v but not %v < %v", a, b, c, a, c)
				}
			}
		}
	}
}

// TestTwoLeavesNeverTie is the case a per-leaf counter cannot answer: two leaves
// minting at the identical clock reading and the identical sequence must still
// order deterministically, and both nodes must agree on which comes first.
func TestTwoLeavesNeverTie(t *testing.T) {
	ts := hlc.Timestamp{Wall: 1_000, Logical: 3}
	a := TxID{HLC: ts, Leaf: leaf(t, "alpha", 1), Seq: 9}
	b := TxID{HLC: ts, Leaf: leaf(t, "beta", 1), Seq: 9}

	if a.Leaf == b.Leaf {
		t.Skip("the two entities collided into one leaf at depth 1; the case needs distinct leaves")
	}
	if a.Compare(b) == 0 {
		t.Fatal("two leaves at the same clock reading compared equal — a cross-leaf AS OF would be undefined")
	}
	if sign(a.Compare(b)) != -sign(b.Compare(a)) {
		t.Fatal("the two leaves do not agree on their order")
	}
}

// TestMinterIsMonotonicPerLeaf checks a minter's output strictly increases,
// including under concurrent callers.
func TestMinterIsMonotonicPerLeaf(t *testing.T) {
	frozen := int64(500)
	clock := hlc.NewClock(func() int64 { return frozen })
	m := NewMinter(leaf(t, "minted", 1), clock)

	const goroutines, each = 8, 150
	var wg sync.WaitGroup
	seen := make([][]TxID, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			out := make([]TxID, each)
			for i := range out {
				out[i] = m.Mint()
			}
			seen[g] = out
		}(g)
	}
	wg.Wait()

	all := make(map[TxID]bool)
	for _, batch := range seen {
		for i, id := range batch {
			if all[id] {
				t.Fatalf("minter issued %v twice", id)
			}
			all[id] = true
			if i > 0 && id.Compare(batch[i-1]) <= 0 {
				t.Fatalf("within one caller, %v does not exceed %v", id, batch[i-1])
			}
		}
	}
	if len(all) != goroutines*each {
		t.Fatalf("got %d distinct identifiers, want %d", len(all), goroutines*each)
	}
}

// TestMintedIdentifiersCarryAdvancingClock checks the clock reading itself
// advances between mints, not merely that identifiers differ.
//
// The distinction is the whole point, and it took a surviving mutant to find
// it. Uniqueness is OVER-DETERMINED here: the clock reading and the sequence are
// each independently sufficient, so replacing the clock reading with a repeated
// one still yields distinct, increasing identifiers and every uniqueness test
// stays green. What breaks is cross-leaf ordering — if every identifier from one
// leaf carried the same reading, ordering between leaves would collapse onto the
// leaf and sequence tie-breakers, which say nothing about when anything happened.
//
// So this asserts the reading, which is the mechanism, rather than the
// uniqueness, which two mechanisms can each supply.
func TestMintedIdentifiersCarryAdvancingClock(t *testing.T) {
	frozen := int64(900)
	m := NewMinter(leaf(t, "clock-advance", 1), hlc.NewClock(func() int64 { return frozen }))

	prev := m.Mint()
	for i := 0; i < 20; i++ {
		got := m.Mint()
		if got.HLC.Compare(prev.HLC) <= 0 {
			t.Fatalf("iteration %d: minted clock reading %v does not exceed the previous %v — "+
				"identifiers would still be distinct via Seq, but cross-leaf ordering would carry no time",
				i, got.HLC, prev.HLC)
		}
		prev = got
	}
}

// TestObserveOrdersAfterARemoteLeaf checks causality crosses a leaf boundary: a
// minter that has seen a remote transaction cannot then mint something that
// appears to precede it.
func TestObserveOrdersAfterARemoteLeaf(t *testing.T) {
	m := NewMinter(leaf(t, "local", 1), hlc.NewClock(func() int64 { return 100 }))
	remote := TxID{
		HLC:  hlc.Timestamp{Wall: 9_000, Logical: 4},
		Leaf: leaf(t, "elsewhere", 1),
		Seq:  1,
	}
	m.Observe(remote)
	if got := m.Mint(); got.Compare(remote) <= 0 {
		t.Fatalf("after observing %v the minter issued %v, which does not follow it", remote, got)
	}
}

// TestMinterRefusesAForeignLeaf checks a minter mints only for its own leaf, so
// the per-leaf sequence cannot be advanced by another leaf's writer.
func TestMinterRefusesAForeignLeaf(t *testing.T) {
	m := NewMinter(leaf(t, "owner", 1), hlc.NewSystemClock())
	id := m.Mint()
	if id.Leaf != m.Leaf() {
		t.Fatalf("minter for %v issued an identifier for %v", m.Leaf(), id.Leaf)
	}
}

// TestEncodingIsByteComparable checks the encoding sorts as bytes in exactly the
// order Compare gives, so a segment index can order on it without decoding.
func TestEncodingIsByteComparable(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	gen := func() TxID {
		return TxID{
			HLC:  hlc.Timestamp{Wall: rng.Int63n(1 << 20), Logical: uint32(rng.Intn(4))},
			Leaf: leaf(t, string(rune('a'+rng.Intn(6))), uint8(1+rng.Intn(3))),
			Seq:  uint32(rng.Intn(4)),
		}
	}
	for i := 0; i < 3000; i++ {
		a, b := gen(), gen()
		ea, eb := a.Encode(), b.Encode()
		if got, want := sign(bytes.Compare(ea[:], eb[:])), sign(a.Compare(b)); got != want {
			t.Fatalf("case %d: bytes sign %d, Compare sign %d\n a=%v\n b=%v", i, got, want, a, b)
		}
	}
}

// TestEncodingIsFixedWidth checks every encoding is the same length, so an index
// can stride over them without a length prefix.
func TestEncodingIsFixedWidth(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		id := TxID{
			HLC:  hlc.Timestamp{Wall: rng.Int63(), Logical: rng.Uint32()},
			Leaf: leaf(t, string(rune('a'+rng.Intn(20))), uint8(1+rng.Intn(32))),
			Seq:  rng.Uint32(),
		}
		if got := id.Encode(); len(got) != EncodedSize {
			t.Fatalf("encoding is %d bytes, want %d", len(got), EncodedSize)
		}
	}
}

// TestEncodingRoundTrips checks an identifier survives the form an index orders
// it by.
func TestEncodingRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 300; i++ {
		want := TxID{
			HLC:  hlc.Timestamp{Wall: rng.Int63n(1 << 40), Logical: rng.Uint32()},
			Leaf: leaf(t, string(rune('a'+rng.Intn(10))), uint8(1+rng.Intn(32))),
			Seq:  rng.Uint32(),
		}
		got, err := Decode(want.Encode())
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got != want {
			t.Fatalf("round trip: got %v, want %v", got, want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
