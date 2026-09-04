package segment

import "fmt"

// EncodeBlock applies the pipeline to a block's raw bytes and returns the header
// describing what it did, together with the bytes to store.
//
// The pipeline order is fixed — compress, then encrypt, then code — and only the
// first stage is implemented here. The header records which stages ran, so a
// reader applies their inverses without being told and later stages can be added
// without changing what an existing block means.
func EncodeBlock(raw []byte, codec CodecID) (BlockHeader, []byte, error) {
	c, err := LookupCodec(codec)
	if err != nil {
		return BlockHeader{}, nil, err
	}

	stored, err := c.Compress(raw)
	if err != nil {
		return BlockHeader{}, nil, fmt.Errorf("segment: %s compress: %w", c.Name(), err)
	}

	h := BlockHeader{
		Codec:     codec,
		Cipher:    CipherNone,
		RawLen:    uint32(len(raw)),
		StoredLen: uint32(len(stored)),
		Checksum:  Checksum(stored),
	}
	if codec != CodecIdentity {
		h.Stages |= StageCompressed
	}
	return h, stored, nil
}

// DecodeBlock reverses [EncodeBlock].
//
// ★ It takes NO configuration. Everything needed to read the block comes from
// the header, which is the property that keeps a settings change from
// reinterpreting data already written — and it is why this signature has no
// third parameter.
//
// ⚠ The checksum is verified BEFORE the codec runs. Handing rotten bytes to a
// decompressor produces a confusing failure at best and plausible garbage at
// worst, and either is worse than a named error.
func DecodeBlock(h BlockHeader, stored []byte) ([]byte, error) {
	if err := h.Verify(stored); err != nil {
		return nil, err
	}
	if got := uint32(len(stored)); got != h.StoredLen {
		return nil, fmt.Errorf("%w: %d stored bytes but the header records %d",
			ErrCorruptBlock, got, h.StoredLen)
	}

	c, err := LookupCodec(h.Codec)
	if err != nil {
		return nil, err
	}

	raw, err := c.Decompress(stored, int(h.RawLen))
	if err != nil {
		return nil, fmt.Errorf("segment: %s decompress: %w", c.Name(), err)
	}
	if got := uint32(len(raw)); got != h.RawLen {
		return nil, fmt.Errorf("%w: decoded %d bytes but the header records %d raw",
			ErrCorruptBlock, got, h.RawLen)
	}
	return raw, nil
}
