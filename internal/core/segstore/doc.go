// Package segstore writes a run of blocks to one file and finds one again by key.
//
// It is the container ADR-005 deliberately did not specify. ADR-005 fixed what a
// BLOCK is — self-describing, checksummed, compressed and one day encrypted — and
// said nothing about how a run of them becomes a file on a disk, or how a reader
// finds the one it wants. This package answers only that.
//
// # When a segment exists
//
// The central rule is not what the file looks like but WHEN it exists. A segment
// is written under a temporary name and published by renaming it into place, so a
// reader that lists a directory sees complete segments and nothing else. That is
// what lets ADR-017's read path take no locks: sealed data is immutable, and a
// half-written file is not addressable rather than being guarded.
//
// A crash therefore leaves a file with no valid trailer, which is NOT A SEGMENT
// rather than a broken one — safe to delete without judgement.
//
// # Layout
//
//	[ segment.Header ][ block ][ block ]…[ index ][ trailer ]
//
// The index is written after the blocks, because its size is not known until they
// are all written; the trailer is fixed width and read from the end of the file,
// so one seek finds it whatever the segment's size. The trailer carries a magic
// number, a format version, where the index is and a checksum over it.
//
// # Reading
//
// A sealed segment is immutable, so a [Reader] maps the whole file and lets the
// kernel decide what stays resident. Any number of readers share one mapping with
// no coordination — the same immutability argument ADR-017 makes about state,
// applied to a file.
//
// Two consequences are worth knowing before using this package:
//
// A block handed back is OWNED by the caller. It is never a view into the
// mapping, because such a view becomes a dangling pointer the moment [Reader.Close]
// unmaps — and it behaves perfectly until then, which is the worst schedule a
// defect can have.
//
// An I/O error on a mapped page raises SIGBUS and kills the process, where a read
// syscall would have returned an error. That is the price of mapping, accepted
// here because a sealed segment is complete and never rewritten, so the realistic
// cause is failing hardware rather than a race.
//
// # Platforms
//
// macOS and Linux. The mapping call is per-platform and there is no fallback, so
// building elsewhere fails to COMPILE — naming the file somebody would have to
// write — rather than building and quietly taking a different read path.
//
// See docs/adr/ADR-024-segment-store.md.
package segstore
