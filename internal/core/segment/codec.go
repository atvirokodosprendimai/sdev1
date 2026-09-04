package segment

import (
	"errors"
	"fmt"
	"sync"
)

// CodecID names the compression a block was written with. It is recorded in
// every block header, never held only in configuration.
type CodecID uint16

const (
	// CodecIdentity stores bytes unchanged. It is always registered, so a build
	// with no compression dependency can still read and write blocks.
	//
	// ⚠ "No compression" is a first-class codec rather than an absent one. An
	// absent codec would have to be represented by a zero value, and a zero
	// value is exactly the unrecorded assumption this format rejects.
	CodecIdentity CodecID = 0

	// CodecZstd is registered when the build includes the codec.
	CodecZstd CodecID = 1
)

// ErrUnknownCodec reports a block naming a codec this build does not have. It is
// returned instead of the stored bytes, because returning them would hand a
// caller compressed data as though it were the value.
var ErrUnknownCodec = errors.New("segment: no codec registered for that identifier")

// ErrDuplicateCodec reports two registrations of one identifier.
var ErrDuplicateCodec = errors.New("segment: codec identifier already registered")

// Codec compresses and decompresses a block's bytes.
//
// The interface is deliberately narrow: byte slices in, byte slices out. A codec
// cannot reach the header, the filesystem or any configuration, so it cannot
// become a second place where a format decision lives.
type Codec interface {
	// Name is used in diagnostics only; the identifier is what is stored.
	Name() string
	Compress(raw []byte) ([]byte, error)
	Decompress(stored []byte, rawLen int) ([]byte, error)
}

var (
	codecsMu sync.RWMutex
	codecs   = map[CodecID]Codec{}
)

// RegisterCodec makes a codec available for reading and writing.
//
// A duplicate identifier is refused rather than overwritten, so a collision
// fails at startup instead of producing blocks that one build can read and
// another cannot.
func RegisterCodec(id CodecID, c Codec) error {
	codecsMu.Lock()
	defer codecsMu.Unlock()
	if existing, ok := codecs[id]; ok {
		return fmt.Errorf("%w: %d is already %s", ErrDuplicateCodec, id, existing.Name())
	}
	codecs[id] = c
	return nil
}

// LookupCodec returns the codec for an identifier.
func LookupCodec(id CodecID) (Codec, error) {
	codecsMu.RLock()
	defer codecsMu.RUnlock()
	c, ok := codecs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownCodec, id)
	}
	return c, nil
}

// RegisteredCodecs returns the identifiers this build can read, for a
// diagnostic.
func RegisteredCodecs() []CodecID {
	codecsMu.RLock()
	defer codecsMu.RUnlock()
	out := make([]CodecID, 0, len(codecs))
	for id := range codecs {
		out = append(out, id)
	}
	return out
}

// identityCodec stores bytes unchanged.
type identityCodec struct{}

func (identityCodec) Name() string { return "identity" }

func (identityCodec) Compress(raw []byte) ([]byte, error) {
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

func (identityCodec) Decompress(stored []byte, rawLen int) ([]byte, error) {
	if len(stored) != rawLen {
		return nil, fmt.Errorf("segment: identity codec: stored %d bytes but header records %d raw",
			len(stored), rawLen)
	}
	out := make([]byte, len(stored))
	copy(out, stored)
	return out, nil
}

func init() {
	// Unconditional: the format must be readable by a build with no compression
	// dependency at all.
	if err := RegisterCodec(CodecIdentity, identityCodec{}); err != nil {
		panic("segment: registering the identity codec: " + err.Error())
	}
}
