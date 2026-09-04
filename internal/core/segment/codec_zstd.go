package segment

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// zstdCodec is the compressing codec. It is registered alongside the identity
// codec rather than replacing it, so a block written without compression stays
// readable by a build that has this codec, and vice versa.
type zstdCodec struct {
	enc *zstd.Encoder
	dec *zstd.Decoder
}

func (zstdCodec) Name() string { return "zstd" }

func (z zstdCodec) Compress(raw []byte) ([]byte, error) {
	return z.enc.EncodeAll(raw, make([]byte, 0, len(raw))), nil
}

func (z zstdCodec) Decompress(stored []byte, rawLen int) ([]byte, error) {
	out, err := z.dec.DecodeAll(stored, make([]byte, 0, rawLen))
	if err != nil {
		return nil, err
	}
	return out, nil
}

func init() {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		panic("segment: building the zstd encoder: " + err.Error())
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic("segment: building the zstd decoder: " + err.Error())
	}
	if err := RegisterCodec(CodecZstd, zstdCodec{enc: enc, dec: dec}); err != nil {
		panic(fmt.Sprintf("segment: registering the zstd codec: %v", err))
	}
}
