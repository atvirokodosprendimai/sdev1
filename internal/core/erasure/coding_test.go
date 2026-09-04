package erasure

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/durability"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// blockHeaderFor builds the block header a coder would be handed: coding is the
// last pipeline stage, so the block given to Encode is the block the segment
// format already checksummed.
func blockHeaderFor(b []byte) segment.BlockHeader {
	return segment.BlockHeader{
		RawLen:    uint32(len(b)),
		StoredLen: uint32(len(b)),
		Checksum:  segment.Checksum(b),
	}
}

func scheme(t *testing.T, k, m uint8) StripeHeader {
	t.Helper()
	h := StripeHeader{DataShards: k, ParityShards: m, Leaf: testLeaf(t), BlockIndex: 1}
	if err := h.Validate(); err != nil {
		t.Fatalf("RS(%d,%d): %v", k, m, err)
	}
	return h
}

// combinations calls fn with every way of choosing r of n indices.
func combinations(n, r int, fn func([]int)) {
	pick := make([]int, r)
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == r {
			fn(pick)
			return
		}
		for i := start; i < n; i++ {
			pick[depth] = i
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
}

// TestEncodeReconstructRoundTrips is the property: encode then reconstruct
// returns the original block, across generated sizes and several schemes.
//
// Sizes that do not divide by k are included deliberately — the padding on the
// last data fragment is where a length is most easily lost.
func TestEncodeReconstructRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	for _, s := range [][2]uint8{{8, 2}, {10, 4}, {4, 2}, {2, 1}, {1, 1}} {
		sc := scheme(t, s[0], s[1])
		for i := 0; i < 40; i++ {
			block := make([]byte, rng.Intn(9000))
			rng.Read(block)
			bh := blockHeaderFor(block)

			st, err := Encode(sc, block, bh)
			if err != nil {
				t.Fatalf("RS(%d,%d) size %d: Encode: %v", s[0], s[1], len(block), err)
			}
			if got, want := len(st.Fragments), int(s[0])+int(s[1]); got != want {
				t.Fatalf("RS(%d,%d): %d fragments, want %d", s[0], s[1], got, want)
			}
			if st.Header.BlockLength != uint32(len(block)) {
				t.Fatalf("block length recorded as %d, want %d", st.Header.BlockLength, len(block))
			}

			got, err := Reconstruct(st.Header, st.BlockHeader, st.Fragments)
			if err != nil {
				t.Fatalf("RS(%d,%d) size %d: Reconstruct: %v", s[0], s[1], len(block), err)
			}
			if !bytes.Equal(got, block) {
				t.Fatalf("RS(%d,%d) size %d: round trip returned %d bytes that differ",
					s[0], s[1], len(block), len(got))
			}
		}
	}
}

// TestAnyMFragmentsMayBeLost checks EVERY combination of m losses reconstructs,
// not one sampled combination — a sampled test can miss the parity positions
// entirely and still look thorough.
func TestAnyMFragmentsMayBeLost(t *testing.T) {
	for _, s := range [][2]uint8{{8, 2}, {4, 2}, {6, 3}} {
		k, m := int(s[0]), int(s[1])
		sc := scheme(t, s[0], s[1])
		block := bytes.Repeat([]byte("planetary scale structural data "), 40)
		bh := blockHeaderFor(block)

		st, err := Encode(sc, block, bh)
		if err != nil {
			t.Fatalf("RS(%d,%d): Encode: %v", k, m, err)
		}

		cases := 0
		combinations(k+m, m, func(lost []int) {
			cases++
			surviving := make([]Fragment, 0, k)
			for i, f := range st.Fragments {
				if !contains(lost, i) {
					surviving = append(surviving, f)
				}
			}
			got, err := Reconstruct(st.Header, st.BlockHeader, surviving)
			if err != nil {
				t.Fatalf("RS(%d,%d): losing fragments %v: %v", k, m, lost, err)
			}
			if !bytes.Equal(got, block) {
				t.Fatalf("RS(%d,%d): losing fragments %v reconstructed different bytes", k, m, lost)
			}
		})
		if cases == 0 {
			t.Fatalf("RS(%d,%d): no loss combinations were tried", k, m)
		}
	}
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestCorruptFragmentIsTreatedAsAbsent is the heart of this record.
//
// A fragment that fails its checksum must be excluded before decoding. Without
// that, the code's tolerance drops from m erasures to ⌊m/2⌋ errors, and a rotten
// fragment produces a block that is wrong with no error raised anywhere.
func TestCorruptFragmentIsTreatedAsAbsent(t *testing.T) {
	const k, m = 4, 2
	sc := scheme(t, k, m)
	block := bytes.Repeat([]byte("a block that will be coded and then damaged "), 20)
	bh := blockHeaderFor(block)

	st, err := Encode(sc, block, bh)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// One fragment rots. Every fragment is still PRESENT, so nothing except the
	// checksum can tell the decoder that one of them is lying.
	damaged := cloneFragments(st.Fragments)
	damaged[1].Bytes = append([]byte(nil), damaged[1].Bytes...)
	damaged[1].Bytes[3] ^= 0xff

	got, err := Reconstruct(st.Header, st.BlockHeader, damaged)
	if err != nil {
		t.Fatalf("a stripe with one rotten fragment and %d survivors should reconstruct: %v", k+m-1, err)
	}
	if !bytes.Equal(got, block) {
		t.Fatal("a rotten fragment was used as data: the reconstructed block differs from the original")
	}

	// At the tolerance boundary, excluding the rotten fragment is the difference
	// between a refusal and a wrong answer. Two lost plus one rotten leaves
	// k-1 = 3 usable fragments, which is not enough.
	atBoundary := cloneFragments(st.Fragments)[2:] // fragments 0 and 1 lost
	atBoundary[0].Bytes = append([]byte(nil), atBoundary[0].Bytes...)
	atBoundary[0].Bytes[0] ^= 0x01

	if _, err := Reconstruct(st.Header, st.BlockHeader, atBoundary); !errors.Is(err, ErrInsufficientFragments) {
		t.Fatalf("two losses plus one corruption: error = %v, want ErrInsufficientFragments — "+
			"a decoder that used the rotten fragment would return wrong bytes instead", err)
	}

	// A fragment of the wrong length is not a fragment of this stripe, whatever
	// its own checksum says about its own bytes.
	wrongSize := cloneFragments(st.Fragments)
	wrongSize[0] = NewFragment(0, []byte("short"))
	if _, err := Reconstruct(st.Header, st.BlockHeader, wrongSize); err != nil {
		t.Fatalf("a wrong-length fragment should be ignored while %d others survive: %v", k+m-1, err)
	}
}

func cloneFragments(in []Fragment) []Fragment {
	out := make([]Fragment, len(in))
	copy(out, in)
	return out
}

// TestReconstructionRefusesBelowK checks that fewer than k verified fragments
// yields a named refusal rather than a best-effort answer.
func TestReconstructionRefusesBelowK(t *testing.T) {
	const k, m = 8, 2
	sc := scheme(t, k, m)
	block := bytes.Repeat([]byte("below the floor "), 100)
	bh := blockHeaderFor(block)

	st, err := Encode(sc, block, bh)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Exactly k survive: the last combination that must work.
	if _, err := Reconstruct(st.Header, st.BlockHeader, st.Fragments[:k]); err != nil {
		t.Fatalf("exactly k=%d fragments must reconstruct: %v", k, err)
	}

	// One fewer: the information is not present.
	for n := k - 1; n >= 0; n-- {
		_, err := Reconstruct(st.Header, st.BlockHeader, st.Fragments[:n])
		if !errors.Is(err, ErrInsufficientFragments) {
			t.Fatalf("%d of %d fragments: error = %v, want ErrInsufficientFragments", n, k+m, err)
		}
	}

	// Duplicates do not count twice towards the floor.
	dupes := []Fragment{st.Fragments[0], st.Fragments[0], st.Fragments[0], st.Fragments[1]}
	if _, err := Reconstruct(st.Header, st.BlockHeader, dupes); !errors.Is(err, ErrInsufficientFragments) {
		t.Errorf("four fragments naming two positions: error = %v, want ErrInsufficientFragments", err)
	}
}

// TestEncodingIsDeterministic checks the same block and scheme give
// byte-identical fragments, so a rebuilt fragment is indistinguishable from the
// one it replaces. Without it, two copies of one fragment can differ and nothing
// can adjudicate between them.
func TestEncodingIsDeterministic(t *testing.T) {
	sc := scheme(t, 8, 2)
	block := bytes.Repeat([]byte("determinism is what makes repair possible "), 30)
	bh := blockHeaderFor(block)

	first, err := Encode(sc, block, bh)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		again, err := Encode(sc, block, bh)
		if err != nil {
			t.Fatalf("Encode attempt %d: %v", attempt, err)
		}
		if again.Header != first.Header {
			t.Fatalf("attempt %d produced a different header:\n got %+v\nwant %+v",
				attempt, again.Header, first.Header)
		}
		for i := range first.Fragments {
			if !bytes.Equal(again.Fragments[i].Bytes, first.Fragments[i].Bytes) {
				t.Fatalf("attempt %d: fragment %d differs from the first encoding", attempt, i)
			}
			if again.Fragments[i].Checksum != first.Fragments[i].Checksum {
				t.Fatalf("attempt %d: fragment %d checksum differs", attempt, i)
			}
		}
	}
}

// TestPolicySelectsTheScheme checks k and m come from the durability policy and
// this package computes no second opinion about them.
//
// It is also the reachability check: SchemeFromPolicy is the one line that makes
// the coder reachable from a policy, and deleting it would leave Encode callable
// only from a test.
func TestPolicySelectsTheScheme(t *testing.T) {
	for _, c := range []struct{ data, parity int }{{8, 2}, {10, 4}, {2, 1}} {
		p, err := durability.Coded(c.data, c.parity, 2, "rack")
		if err != nil {
			t.Fatalf("durability.Coded(%d,%d): %v", c.data, c.parity, err)
		}
		h, err := SchemeFromPolicy(p)
		if err != nil {
			t.Fatalf("SchemeFromPolicy(%s): %v", p, err)
		}
		if int(h.DataShards) != c.data || int(h.ParityShards) != c.parity {
			t.Errorf("policy RS(%d,%d) produced scheme RS(%d,%d) — the scheme must come from "+
				"the policy, not from a second definition here",
				c.data, c.parity, h.DataShards, h.ParityShards)
		}
		if h.Fragments() != p.DomainsNeeded() {
			t.Errorf("scheme needs %d fragments but the policy needs %d failure domains; "+
				"these must be the same number, decided once in ADR-004",
				h.Fragments(), p.DomainsNeeded())
		}
	}

	// A replicated policy names no code, and is refused rather than defaulted.
	rep, err := durability.Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("durability.Replicated: %v", err)
	}
	if _, err := SchemeFromPolicy(rep); !errors.Is(err, ErrInvalidScheme) {
		t.Errorf("a replicated policy: error = %v, want ErrInvalidScheme", err)
	}
}

// TestCodedBlocksAreFlagged checks the coder records the stage in the block's
// own header, and that the end-to-end check after reassembly is real.
//
// A segment may hold coded and uncoded blocks together, so nothing may infer
// from a sealed segment that its blocks are coded.
func TestCodedBlocksAreFlagged(t *testing.T) {
	sc := scheme(t, 4, 2)
	block := bytes.Repeat([]byte("flag me "), 50)
	bh := blockHeaderFor(block)
	bh.Stages = segment.StageCompressed // an earlier stage already ran

	st, err := Encode(sc, block, bh)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !st.BlockHeader.Stages.Has(segment.StageCoded) {
		t.Error("the coder did not record StageCoded; a reader is left assuming a sealed block is coded")
	}
	if !st.BlockHeader.Stages.Has(segment.StageCompressed) {
		t.Error("the coder dropped an earlier stage flag")
	}
	if bh.Stages.Has(segment.StageCoded) {
		t.Error("the caller's own header was modified; the flag must travel on the returned header " +
			"so it cannot be taken and dropped")
	}

	// The end-to-end check: a header whose checksum does not describe the block
	// is caught after reassembly, which is the only check spanning the path.
	wrong := st.BlockHeader
	wrong.Checksum ^= 0xffffffff
	if _, err := Reconstruct(st.Header, wrong, st.Fragments); !errors.Is(err, segment.ErrCorruptBlock) {
		t.Errorf("a reassembled block that fails its block checksum: error = %v, want ErrCorruptBlock", err)
	}
}
