// Package segment defines what is actually on disk: immutable segments holding
// many independently coded blocks.
//
// # A block is interpretable from its own bytes
//
// Every block header carries the codec, the cipher, which pipeline stages ran,
// both lengths, and a checksum. Reading a block needs nothing from
// configuration.
//
// That is the single most important property here, and it is why the codec is
// recorded rather than configured. A block written with one codec is only
// readable by something that knows which; holding that choice in settings alone
// means a settings change silently reinterprets data already written. A query
// clause may choose the codec for NEW blocks — it can never change what an
// existing block means.
//
// # The pipeline order is fixed, not tuned
//
// Compress, then encrypt, then erasure-code.
//
// Encrypting first would destroy compressibility, because ciphertext does not
// compress — the stage would cost CPU and save nothing. Coding first would mean
// compressing parity, which is waste and breaks the fragment structure. The
// header records which stages ran, so a reader applies their inverses in the
// right order without being told and cannot guess wrong.
//
// # How it fails, and how it recovers
//
// A CORRUPT BLOCK is detected rather than returned. Every block carries a
// checksum over its STORED bytes — after compression and encryption, because
// those are the bytes a disk can actually rot — and it is verified BEFORE the
// codec runs, since handing rotten bytes to a decompressor produces a confusing
// failure at best and plausible garbage at worst. Recovery is to fetch the block
// from another copy; this package's job is to make the fault visible.
//
// ⚠ That check is a prerequisite for erasure coding rather than a nicety.
// Erasure decoding assumes it knows WHICH fragments are missing, so a
// present-but-rotten fragment fed to a decoder returns wrong data with no error
// anywhere. Without a per-block checksum there is nothing to notice.
//
// An UNKNOWN FORMAT VERSION is refused rather than partially read, so an
// incompatible change becomes a migration instead of a corruption.
//
// An UNREGISTERED CODEC is refused by name. A build lacking a compressor can
// still read and write blocks through the identity codec, and meeting a codec it
// does not have produces an error rather than the stored bytes returned as
// though they were the value.
//
// # What this package deliberately does not do
//
// It never opens a file. It works on byte slices and headers, because the format
// is what must be right before anything reaches a disk, and keeping I/O out is
// what makes every property above testable with no storage engine.
//
// The decision this package implements is recorded in
// docs/adr/ADR-005-segment-format.md.
package segment
