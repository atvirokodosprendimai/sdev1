package erasure

import (
	"errors"
	"fmt"

	"github.com/klauspost/reedsolomon"

	"github.com/atvirokodosprendimai/sdev1/internal/core/durability"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// ErrInsufficientFragments reports that fewer than k fragments verified.
//
// It is returned instead of a partial reconstruction because below k the
// information is not present. There is no best effort here — there is only
// invention, and invention returned as data is worse than a refusal.
var ErrInsufficientFragments = errors.New("erasure: too few verified fragments to reconstruct")

// Stripe is one block's coded form: the scheme that produced it, the block
// header the coder annotated, and the fragments themselves.
//
// ★ The block header is returned rather than modified through a pointer so a
// caller cannot take the fragments and drop the flag that says the block is
// coded. A block flagged coded whose stripe is missing, or a coded block not
// flagged, is unreadable in a way neither this record nor ADR-005 catches alone.
type Stripe struct {
	Header      StripeHeader
	BlockHeader segment.BlockHeader
	Fragments   []Fragment
}

// SchemeFromPolicy turns a durability policy into a coding scheme.
//
// ★ This is the only place a k and an m enter this package. ADR-004 already
// decides how many failure domains a scheme needs and refuses a policy the
// topology cannot satisfy; a second opinion here would be a cluster that codes
// data it cannot place, so this function reads the policy's shard counts and
// computes nothing about domains.
func SchemeFromPolicy(p durability.Policy) (StripeHeader, error) {
	if !p.IsCoded() {
		return StripeHeader{}, fmt.Errorf("%w: %s is a replicated policy and names no code",
			ErrInvalidScheme, p)
	}
	// Guarding the conversion, not re-deciding the limit: a shard count that
	// does not fit in a byte would wrap silently into a valid-looking scheme.
	if p.DataShards < 0 || p.ParityShards < 0 || p.DataShards+p.ParityShards > MaxCodePositions {
		return StripeHeader{}, fmt.Errorf("%w: %d data plus %d parity, and the field allows %d",
			ErrSchemeTooWide, p.DataShards, p.ParityShards, MaxCodePositions)
	}
	h := StripeHeader{
		DataShards:   uint8(p.DataShards),
		ParityShards: uint8(p.ParityShards),
	}
	if err := h.Validate(); err != nil {
		return StripeHeader{}, err
	}
	return h, nil
}

// Encode splits a block into k data fragments and computes m parity fragments.
//
// The scheme supplies k, m, the leaf and the block index; the fragment size and
// the block's true length are computed here and recorded in the returned header,
// so removing the padding later never depends on knowing how the code pads.
//
// Encoding is deterministic: the same block and scheme give byte-identical
// fragments on any node. Repair depends on it — a rebuilt fragment must be
// indistinguishable from the one it replaces, or two copies of one fragment
// differ and nothing can adjudicate.
func Encode(scheme StripeHeader, block []byte, bh segment.BlockHeader) (Stripe, error) {
	if err := scheme.Validate(); err != nil {
		return Stripe{}, err
	}
	k, m := int(scheme.DataShards), int(scheme.ParityShards)

	fragSize := (len(block) + k - 1) / k
	if fragSize == 0 {
		// The code needs a non-empty shard. BlockLength records that the block
		// held nothing, so the padding is still removable without guessing.
		fragSize = 1
	}

	h := scheme
	h.FragmentSize = uint32(fragSize)
	h.BlockLength = uint32(len(block))

	shards := make([][]byte, k+m)
	for i := range shards {
		shards[i] = make([]byte, fragSize)
	}
	for i := 0; i < k; i++ {
		if lo := i * fragSize; lo < len(block) {
			copy(shards[i], block[lo:min(lo+fragSize, len(block))])
		}
	}

	enc, err := reedsolomon.New(k, m)
	if err != nil {
		return Stripe{}, fmt.Errorf("erasure: building the RS(%d,%d) coder: %w", k, m, err)
	}
	if err := enc.Encode(shards); err != nil {
		return Stripe{}, fmt.Errorf("erasure: coding RS(%d,%d): %w", k, m, err)
	}

	frags := make([]Fragment, k+m)
	for i, s := range shards {
		frags[i] = NewFragment(uint8(i), s)
	}

	// The block is coded; say so in its own header. A segment may hold coded and
	// uncoded blocks together, so nothing may infer this from the segment.
	bh.Stages |= segment.StageCoded

	return Stripe{Header: h, BlockHeader: bh, Fragments: frags}, nil
}

// Reconstruct rebuilds a block from whatever fragments survive.
//
// ⚠ Every fragment is verified BEFORE any decoding, and one that fails is
// treated as ABSENT rather than as data. That is what makes the scheme's stated
// tolerance true: a code with m parity fragments corrects m fragments known to
// be missing, but only half that many that are present and wrong, because
// locating a fault costs as much redundancy as repairing it.
//
// ★ It takes no configuration. Everything needed comes from the stripe header,
// which is why a change to the cluster's configured scheme cannot orphan a
// stripe written under the old one.
func Reconstruct(h StripeHeader, bh segment.BlockHeader, fragments []Fragment) ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	k, m := int(h.DataShards), int(h.ParityShards)

	shards := make([][]byte, k+m)
	verified := 0
	for _, f := range fragments {
		if int(f.Index) >= k+m || shards[f.Index] != nil {
			continue
		}
		// A fragment of the wrong length cannot be a fragment of this stripe,
		// whatever its checksum says about its own bytes.
		if uint32(len(f.Bytes)) != h.FragmentSize {
			continue
		}
		if f.Verify() != nil {
			continue
		}
		shards[f.Index] = f.Bytes
		verified++
	}
	if verified < k {
		return nil, fmt.Errorf("%w: %d of %d fragments verified, and RS(%d,%d) needs %d",
			ErrInsufficientFragments, verified, k+m, k, m, k)
	}

	enc, err := reedsolomon.New(k, m)
	if err != nil {
		return nil, fmt.Errorf("erasure: building the RS(%d,%d) coder: %w", k, m, err)
	}
	if err := enc.ReconstructData(shards); err != nil {
		return nil, fmt.Errorf("erasure: reconstructing RS(%d,%d): %w", k, m, err)
	}

	block := make([]byte, 0, int(h.FragmentSize)*k)
	for i := 0; i < k; i++ {
		block = append(block, shards[i]...)
	}
	if uint32(len(block)) < h.BlockLength {
		return nil, fmt.Errorf("%w: reassembled %d bytes but the stripe records a block of %d",
			segment.ErrCorruptBlock, len(block), h.BlockLength)
	}
	block = block[:h.BlockLength]

	// The end-to-end check. It should be redundant given that every surviving
	// fragment verified, and it is kept because it is the only check spanning
	// the whole path: a coding-matrix fault passes every local check and fails
	// exactly here.
	if err := bh.Verify(block); err != nil {
		return nil, err
	}
	return block, nil
}
