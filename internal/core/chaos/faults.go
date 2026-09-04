package chaos

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"

	"github.com/atvirokodosprendimai/sdev1/internal/core/durability"
	"github.com/atvirokodosprendimai/sdev1/internal/core/erasure"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// sampleBlock is the payload every stripe fault codes. It is deliberately
// compressible and repetitive so a wrong reconstruction is obvious rather than
// plausible.
func sampleBlock(rng *rand.Rand) []byte {
	n := 2000 + rng.Intn(6000)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (i % 23))
	}
	return b
}

func codedStripe(rng *rand.Rand, k, m int) (erasure.Stripe, []byte, error) {
	policy, err := durability.Coded(k, m, 2, "rack")
	if err != nil {
		return erasure.Stripe{}, nil, fmt.Errorf("building the policy: %w", err)
	}
	scheme, err := erasure.SchemeFromPolicy(policy)
	if err != nil {
		return erasure.Stripe{}, nil, fmt.Errorf("scheme from policy: %w", err)
	}
	block := sampleBlock(rng)
	bh := segment.BlockHeader{
		RawLen:    uint32(len(block)),
		StoredLen: uint32(len(block)),
		Checksum:  segment.Checksum(block),
	}
	st, err := erasure.Encode(scheme, block, bh)
	if err != nil {
		return erasure.Stripe{}, nil, fmt.Errorf("encoding: %w", err)
	}
	return st, block, nil
}

// dropFragments removes n fragments chosen by the schedule's generator.
func dropFragments(rng *rand.Rand, frags []erasure.Fragment, n int) []erasure.Fragment {
	order := rng.Perm(len(frags))
	drop := map[int]bool{}
	for i := 0; i < n && i < len(order); i++ {
		drop[order[i]] = true
	}
	out := make([]erasure.Fragment, 0, len(frags)-n)
	for i, f := range frags {
		if !drop[i] {
			out = append(out, f)
		}
	}
	return out
}

func init() {
	must := func(f Fault) {
		if err := Register(f); err != nil {
			panic("chaos: registering " + f.Name + ": " + err.Error())
		}
	}

	// ── ADR-006, the coding tolerance ───────────────────────────────────────

	must(Fault{
		Name:     "fragment-loss-within-tolerance",
		Record:   "ADR-006",
		Expected: Recovers,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			const k, m = 4, 2
			st, block, err := codedStripe(rng, k, m)
			if err != nil {
				return Outcome{}, err
			}
			survivors := dropFragments(rng, st.Fragments, m)
			if len(survivors) != k {
				return Outcome{}, fmt.Errorf("%w: %d survivors, expected %d",
					ErrPreconditionNotMet, len(survivors), k)
			}
			got, err := erasure.Reconstruct(st.Header, st.BlockHeader, survivors)
			if err != nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("losing %d of %d fragments refused: %v", m, k+m, err)}, nil
			}
			if !bytes.Equal(got, block) {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: "reconstruction succeeded but returned different bytes"}, nil
			}
			return Outcome{Disposition: Recovers,
				Detail: fmt.Sprintf("lost %d of %d fragments; the block was rebuilt byte-identical from %d survivors",
					m, k+m, len(survivors))}, nil
		},
	})

	must(Fault{
		Name:     "fragment-loss-beyond-tolerance",
		Record:   "ADR-006",
		Expected: UnrecoverableByDesign,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			const k, m = 4, 2
			st, _, err := codedStripe(rng, k, m)
			if err != nil {
				return Outcome{}, err
			}
			survivors := dropFragments(rng, st.Fragments, m+1)
			if len(survivors) != k-1 {
				return Outcome{}, fmt.Errorf("%w: %d survivors, expected %d",
					ErrPreconditionNotMet, len(survivors), k-1)
			}
			got, err := erasure.Reconstruct(st.Header, st.BlockHeader, survivors)
			if err == nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("reconstruction returned %d bytes from %d of %d fragments; "+
						"the information was not present, so this is invention", len(got), len(survivors), k+m)}, nil
			}
			if !errors.Is(err, erasure.ErrInsufficientFragments) {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("refused, but not by name: %v", err)}, nil
			}
			return Outcome{Disposition: UnrecoverableByDesign,
				Detail: fmt.Sprintf("lost %d of %d fragments, leaving %d and needing %d; refused with "+
					"ErrInsufficientFragments rather than returning anything", m+1, k+m, len(survivors), k)}, nil
		},
	})

	must(Fault{
		Name:     "fragment-corruption",
		Record:   "ADR-006",
		Expected: Recovers,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			const k, m = 4, 2
			st, block, err := codedStripe(rng, k, m)
			if err != nil {
				return Outcome{}, err
			}
			victim := rng.Intn(len(st.Fragments))
			frags := make([]erasure.Fragment, len(st.Fragments))
			copy(frags, st.Fragments)
			rotten := append([]byte(nil), frags[victim].Bytes...)
			rotten[rng.Intn(len(rotten))] ^= 1 << uint(rng.Intn(8))
			frags[victim].Bytes = rotten

			// The precondition: the damage must actually be detectable, or what
			// follows is a test of nothing.
			if err := frags[victim].Verify(); err == nil {
				return Outcome{}, fmt.Errorf("%w: fragment %d still verifies after being altered",
					ErrPreconditionNotMet, victim)
			}

			got, err := erasure.Reconstruct(st.Header, st.BlockHeader, frags)
			if err != nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("one corrupt fragment of %d refused the whole stripe: %v", k+m, err)}, nil
			}
			if !bytes.Equal(got, block) {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: "a corrupt fragment was used as data: the rebuilt block differs from the original"}, nil
			}
			return Outcome{Disposition: Recovers,
				Detail: fmt.Sprintf("fragment %d was altered and failed its checksum; it was excluded as an "+
					"erasure and the block was rebuilt byte-identical from the remaining %d", victim, k+m-1)}, nil
		},
	})

	// ── ADR-005, the block checksum ─────────────────────────────────────────

	must(Fault{
		Name:     "block-checksum-mismatch",
		Record:   "ADR-005",
		Expected: Recovers,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			block := sampleBlock(rng)
			h, stored, err := segment.EncodeBlock(block, segment.CodecZstd)
			if err != nil {
				return Outcome{}, fmt.Errorf("encoding the block: %w", err)
			}
			rotten := append([]byte(nil), stored...)
			at := rng.Intn(len(rotten))
			rotten[at] ^= 1 << uint(rng.Intn(8))
			if bytes.Equal(rotten, stored) {
				return Outcome{}, fmt.Errorf("%w: the block was not actually altered", ErrPreconditionNotMet)
			}

			got, err := segment.DecodeBlock(h, rotten)
			if err == nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("a block with a flipped bit at byte %d decoded to %d bytes "+
						"and reported success", at, len(got))}, nil
			}
			if !errors.Is(err, segment.ErrCorruptBlock) {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("refused, but not by name: %v", err)}, nil
			}
			return Outcome{Disposition: Recovers,
				Detail: fmt.Sprintf("a flipped bit at byte %d of %d was detected before the codec ran; "+
					"ErrCorruptBlock was returned instead of the bytes", at, len(stored))}, nil
		},
	})

	// ── ADR-004, the refusal floor ──────────────────────────────────────────

	must(Fault{
		Name:     "durability-floor-breached",
		Record:   "ADR-004",
		Expected: Recovers,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			policy, err := durability.Coded(4, 2, 4, "rack")
			if err != nil {
				return Outcome{}, fmt.Errorf("building the policy: %w", err)
			}
			// Degrade the cluster: fewer distinct domains survive than the floor.
			healthy := []string{"rack-a", "rack-b", "rack-c", "rack-d"}
			if err := policy.Satisfied(healthy); err != nil {
				return Outcome{}, fmt.Errorf("%w: a healthy cluster of %d domains does not satisfy a floor of %d: %v",
					ErrPreconditionNotMet, len(healthy), policy.MinSize, err)
			}
			degraded := healthy[:policy.MinSize-1]
			if err := policy.Satisfied(degraded); err == nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("%d domains satisfied a floor of %d; the cluster would accept writes "+
						"at a durability nobody has", len(degraded), policy.MinSize)}, nil
			}
			// The trap the floor exists for: repeated domains are not copies.
			repeated := []string{"rack-a", "rack-a", "rack-a", "rack-a"}
			if err := policy.Satisfied(repeated); err == nil {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: "four copies in one failure domain satisfied a floor of four; " +
						"the floor is counting copies rather than domains"}, nil
			}
			return Outcome{Disposition: Recovers,
				Detail: fmt.Sprintf("with %d of %d domains surviving against a floor of %d, writes are refused; "+
					"and %d copies in ONE domain are also refused, so the floor counts distinct domains",
					len(degraded), len(healthy), policy.MinSize, len(repeated))}, nil
		},
	})

	// ── ADR-017, the live tail ──────────────────────────────────────────────

	must(Fault{
		Name:     "writer-stopped-mid-append",
		Record:   "ADR-017",
		Expected: Recovers,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			tl := tail.New()
			w, ok := tl.TakeWriter()
			if !ok {
				return Outcome{}, fmt.Errorf("%w: could not take the writer on a fresh tail", ErrPreconditionNotMet)
			}
			published := 3 + rng.Intn(40)
			for seq := 1; seq <= published; seq++ {
				id := tx.TxID{HLC: hlc.Timestamp{Wall: int64(seq) * 1000, Logical: uint32(seq)}, Seq: uint32(seq)}
				d := []ports.Datom{{Entity: fmt.Sprintf("e%d", seq), Attribute: "a", Assert: true}}
				if _, err := tl.Append(w, id, d); err != nil {
					return Outcome{}, fmt.Errorf("appending %d: %w", seq, err)
				}
			}

			// The writer dies here. Nothing releases anything, nothing is
			// flushed, no cleanup runs — which is what a process being killed
			// looks like from the tail's point of view.
			mark := tl.Watermark()
			if uint64(mark) != uint64(published) {
				return Outcome{}, fmt.Errorf("%w: watermark %d after %d appends",
					ErrPreconditionNotMet, mark, published)
			}

			seen := 0
			intact := true
			tl.Walk(mark, func(e tail.Entry) bool {
				seen++
				if len(e.Datoms) != 1 || e.Datoms[0].Entity != fmt.Sprintf("e%d", e.TxID.Seq) {
					intact = false
					return false
				}
				return true
			})
			if !intact {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: "an entry published before the writer stopped is incomplete"}, nil
			}
			if seen != published {
				return Outcome{Disposition: UnrecoverableAndOpen,
					Detail: fmt.Sprintf("%d of %d published entries survived the writer stopping", seen, published)}, nil
			}
			return Outcome{Disposition: Recovers,
				Detail: fmt.Sprintf("the writer stopped after publishing %d entries; all %d are readable and whole, "+
					"and anything it had not published was never reachable", published, seen)}, nil
		},
	})

	must(Fault{
		Name:     "writer-process-lost",
		Record:   "ADR-017",
		Expected: UnrecoverableAndOpen,
		Inject: func(rng *rand.Rand) (Outcome, error) {
			tl := tail.New()
			w, ok := tl.TakeWriter()
			if !ok {
				return Outcome{}, fmt.Errorf("%w: could not take the writer on a fresh tail", ErrPreconditionNotMet)
			}
			id := tx.TxID{HLC: hlc.Timestamp{Wall: 1000, Logical: 1}, Seq: 1}
			if _, err := tl.Append(w, id, []ports.Datom{{Entity: "e1", Attribute: "a", Assert: true}}); err != nil {
				return Outcome{}, fmt.Errorf("the first append failed: %w", err)
			}

			// The writer's process is lost. The token goes with it.
			w = tail.WriterToken{}

			// Reads still work — that is the half that recovers.
			readable := 0
			tl.Walk(tl.Watermark(), func(tail.Entry) bool { readable++; return true })
			if readable != 1 {
				return Outcome{}, fmt.Errorf("%w: %d entries readable, expected 1", ErrPreconditionNotMet, readable)
			}

			// Writes do not. Nothing can take the token, and the lost one is
			// refused.
			if _, again := tl.TakeWriter(); again {
				return Outcome{Disposition: Recovers,
					Detail: "a replacement writer took the token after the first was lost"}, nil
			}
			nextID := tx.TxID{HLC: hlc.Timestamp{Wall: 2000, Logical: 2}, Seq: 2}
			_, err := tl.Append(w, nextID, []ports.Datom{{Entity: "e2", Attribute: "a", Assert: true}})
			if !errors.Is(err, tail.ErrWriterNotHeld) {
				return Outcome{Disposition: Recovers,
					Detail: fmt.Sprintf("an append after the writer was lost returned %v", err)}, nil
			}
			return Outcome{Disposition: UnrecoverableAndOpen,
				Detail: "the leaf is permanently read-only: reads still serve the published prefix, but " +
					"TakeWriter refuses forever and no append can succeed. There is no handover, and adding " +
					"a bare release would be worse — two writers computing the same slot. Fencing is what " +
					"makes handover safe, and ADR-009 owns it."}, nil
		},
	})
}
