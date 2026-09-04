package temporal

import (
	"math/rand"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// This suite exists because of a measured failure, not a hypothetical one.
//
// A sibling project shipped the two-axis defect past roughly 140 tests
// including a race detector, because every one of its tests happened to write
// with validity beginning at commit time — so its two visibility parameters
// were never actually different in any test, and the bug had structurally no
// test that could see it however many tests existed.
//
// A hand-written fixture encodes what its author expected and therefore cannot
// falsify the expectation. Only generated divergence can, which is why the
// generator below draws business time and transaction time INDEPENDENTLY.

const divergenceSeed = 20260904

// genCase is one generated datom and the query asked about it.
type genCase struct {
	ValidFrom  int64
	ValidTo    int64
	ID         tx.TxID
	Query      Query
	CommitWall int64
}

// backdated reports whether this case's business time begins before the instant
// it was committed at — the shape that broke the sibling project.
func (c genCase) backdated() bool { return c.ValidFrom < c.CommitWall }

// futureDated reports whether validity begins after the commit.
func (c genCase) futureDated() bool { return c.ValidFrom > c.CommitWall }

// generate draws cases whose two time axes are independent.
//
// ⚠ The independence is the whole mechanism. If business time is ever derived
// from commit time "for realism", this suite stops being able to see the defect
// it exists for, and every assertion below keeps passing.
func generate(rng *rand.Rand, n int, leafID addr.LeafID) []genCase {
	cases := make([]genCase, 0, n)
	for i := 0; i < n; i++ {
		commit := rng.Int63n(10_000)
		validFrom := rng.Int63n(10_000) // drawn independently of commit
		validTo := validFrom + 1 + rng.Int63n(5_000)
		if rng.Intn(5) == 0 {
			validTo = Forever
		}

		c := genCase{
			ValidFrom:  validFrom,
			ValidTo:    validTo,
			CommitWall: commit,
			ID: tx.TxID{
				HLC:  hlc.Timestamp{Wall: commit, Logical: uint32(rng.Intn(3))},
				Leaf: leafID,
				Seq:  uint32(rng.Intn(5)),
			},
		}

		// A query with each qualifier independently present or absent, so all
		// four rows of the defaults table are exercised against real datoms.
		//
		// ⚠ One in three business instants is drawn EXACTLY ON a boundary of
		// the datom's own interval. Uniform random instants over a wide range
		// essentially never land on validFrom or validTo, so a corpus without
		// this cannot observe a half-open/closed confusion at all — a mutation
		// breaking the boundary survived until these cases were added. A
		// fixture that cannot produce the failure is unfalsifiable however many
		// cases it holds.
		var q Query
		if rng.Intn(2) == 0 {
			var v int64
			switch rng.Intn(3) {
			case 0:
				v = validFrom // the inclusive edge
			case 1:
				if validTo == Forever {
					v = validFrom + 1
				} else {
					v = validTo // the EXCLUSIVE edge
				}
			default:
				v = rng.Int63n(12_000)
			}
			q.ValidAt = &v
		}
		if rng.Intn(2) == 0 {
			cutoff := tx.TxID{
				HLC:  hlc.Timestamp{Wall: rng.Int63n(10_000)},
				Leaf: leafID,
				Seq:  uint32(rng.Intn(5)),
			}
			q.AsOf = &cutoff
		}
		c.Query = q
		cases = append(cases, c)
	}
	return cases
}

func genLeaf(t *testing.T) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf("divergence"), 1)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	return l
}

// TestGeneratorProducesDivergentCases checks the corpus actually contains the
// shapes it is supposed to: backdated, future-dated, and agreeing.
func TestGeneratorProducesDivergentCases(t *testing.T) {
	rng := rand.New(rand.NewSource(divergenceSeed))
	cases := generate(rng, 2000, genLeaf(t))

	var back, fwd int
	for _, c := range cases {
		switch {
		case c.backdated():
			back++
		case c.futureDated():
			fwd++
		}
	}
	if back < len(cases)/5 {
		t.Errorf("only %d of %d cases are backdated; the corpus barely exercises the shape "+
			"that broke the sibling project", back, len(cases))
	}
	if fwd < len(cases)/5 {
		t.Errorf("only %d of %d cases are future-dated", fwd, len(cases))
	}
}

// TestNoGeneratedCaseHasAgreeingAxes is the guard ON the guard.
//
// A property suite whose generator is too tame is indistinguishable from the
// hand-written fixtures it replaced: green, and blind to the same defect. This
// fails if the corpus degenerates to cases where the two axes always agree.
func TestNoGeneratedCaseHasAgreeingAxes(t *testing.T) {
	rng := rand.New(rand.NewSource(divergenceSeed))
	cases := generate(rng, 2000, genLeaf(t))

	diverging := 0
	for _, c := range cases {
		if c.backdated() || c.futureDated() {
			diverging++
		}
	}
	if diverging == 0 {
		t.Fatal("the generator produced no case where business time and transaction time differ — " +
			"this suite is now exactly the blind fixture set it exists to replace")
	}
	// A corpus that is overwhelmingly agreeing is nearly as bad as one that is
	// entirely agreeing, so require a real majority to diverge.
	if diverging < len(cases)*3/4 {
		t.Errorf("only %d of %d generated cases diverge; the generator has become too tame to "+
			"falsify the axis-conflation defect", diverging, len(cases))
	}
}

// TestVisibleAgreesWithOracle is the property itself: across generated cases
// with independently drawn axes, Visible must agree with a reference written
// from the record rather than from the implementation.
func TestVisibleAgreesWithOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(divergenceSeed))
	cases := generate(rng, 5000, genLeaf(t))
	const now = int64(6_000)

	for i, c := range cases {
		resolved := ResolveQualifiers(c.Query, now)
		got := Visible(c.ValidFrom, c.ValidTo, c.ID, resolved)
		want := oracleVisible(c.ValidFrom, c.ValidTo, c.ID, resolved)
		if got != want {
			t.Fatalf("seed %d, case %d: Visible = %v, oracle = %v\n"+
				"  validity [%d, %d)  committed at %d  backdated=%v\n"+
				"  query validAt=%v asOf=%v\n"+
				"  reproduce with: go test -run TestVisibleAgreesWithOracle (seed is fixed at %d)",
				divergenceSeed, i, got, want,
				c.ValidFrom, c.ValidTo, c.CommitWall, c.backdated(),
				resolved.ValidAt, resolved.AsOf, divergenceSeed)
		}
	}
}

// TestCounterexampleIsReproducible checks the generator is deterministic, so a
// discovered counterexample can be replayed rather than becoming folklore.
func TestCounterexampleIsReproducible(t *testing.T) {
	leafID := genLeaf(t)
	first := generate(rand.New(rand.NewSource(divergenceSeed)), 200, leafID)
	second := generate(rand.New(rand.NewSource(divergenceSeed)), 200, leafID)

	if len(first) != len(second) {
		t.Fatalf("two runs of the same seed produced %d and %d cases", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.ValidFrom != b.ValidFrom || a.ValidTo != b.ValidTo || a.CommitWall != b.CommitWall {
			t.Fatalf("case %d differs between two runs of seed %d — a counterexample could not be replayed",
				i, divergenceSeed)
		}
		if (a.Query.ValidAt == nil) != (b.Query.ValidAt == nil) ||
			(a.Query.AsOf == nil) != (b.Query.AsOf == nil) {
			t.Fatalf("case %d's query shape differs between two runs of seed %d", i, divergenceSeed)
		}
	}
}
