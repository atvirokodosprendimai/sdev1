package topology

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrBadGeneration reports an authored generation that will not decode.
//
// ⚠ It is a refusal rather than a map that silently has none. A generation that
// was written and misread is exactly the case where somebody believes their
// placements are reproducible and they are not.
var ErrBadGeneration = errors.New("topology: the generation is not an encoded transaction identifier")

// Placeable reports whether the map can be placed against.
//
// ★ It gives the question one spelling. A caller comparing against a zero
// identifier by hand is a caller who might compare against the wrong thing, and
// the wrong answer here is "yes" for every map that has no identity at all.
func (m Map) Placeable() bool { return m.Generation != (tx.TxID{}) }

// decodeGeneration reads the authored form: the hex of a transaction
// identifier's fixed-width encoding.
//
// ★ One representation, because [tx] already has a canonical one. A second
// spelling of an identifier is a second thing to keep in step.
//
// ⚠ An EMPTY field is not an error. A map may be loaded without a generation —
// inspecting a cluster's shape is a legitimate thing to do with a file nobody
// published — and the refusal lands at placement, where the consequence is.
func decodeGeneration(authored string) (tx.TxID, error) {
	if authored == "" {
		return tx.TxID{}, nil
	}
	raw, err := hex.DecodeString(authored)
	if err != nil {
		return tx.TxID{}, fmt.Errorf("%w: %v", ErrBadGeneration, err)
	}
	if len(raw) != tx.EncodedSize {
		return tx.TxID{}, fmt.Errorf("%w: %d bytes, want %d",
			ErrBadGeneration, len(raw), tx.EncodedSize)
	}
	var b [tx.EncodedSize]byte
	copy(b[:], raw)
	id, err := tx.Decode(b)
	if err != nil {
		return tx.TxID{}, fmt.Errorf("%w: %v", ErrBadGeneration, err)
	}
	if id == (tx.TxID{}) {
		return tx.TxID{}, fmt.Errorf("%w: it encodes the zero identifier, which is how a map "+
			"with no generation is represented", ErrBadGeneration)
	}
	return id, nil
}

// EncodeGeneration renders a generation in the authored form.
//
// It is here so an operator publishing a map has one way to write the identifier
// down, and it is the exact inverse of what [Load] reads.
func EncodeGeneration(id tx.TxID) string {
	b := id.Encode()
	return hex.EncodeToString(b[:])
}
