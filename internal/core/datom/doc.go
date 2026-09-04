// Package datom turns facts into bytes and back.
//
// It is the layout ADR-005 and ADR-024 deliberately did not specify. ADR-005
// fixed what a BLOCK is and ADR-024 what a SEGMENT is; neither says what goes
// inside one, and until this package existed a [ports.Datom] lived only as a Go
// struct — the engine could write bytes it could not fill.
//
// # The hazard this package exists to refuse
//
// ⚠ A retraction is a datom with Assert CLEARED, never an absent datom. ADR-003
// fixed that, because "this stopped being true" and "this was never recorded"
// are different facts. Go's zero value for a bool is false.
//
// So a decoder that tolerates a short buffer and returns what it managed to fill
// does not return a partial answer. It returns a RETRACTION of a fact that was
// asserted, it reports success, and nothing about the result looks damaged.
//
// Every refusal here is therefore total: [Decode] returns no datoms at all when
// it returns an error, not even the ones that decoded cleanly before the damage.
//
// # The unit is a run
//
//	[ version ][ count ][ datom ][ datom ]…
//
// A format version per datom would cost a byte on every fact for something that
// is constant across millions of them; a version nowhere would make the bytes
// unreadable by a later build. The run header carries it once, and the run stays
// self-contained — which is the level at which ADR-005's rule is stated: a block
// is readable from its own bytes, and a run is what a block holds.
//
// Every field is written on every datom, whatever its value. No field is omitted
// for being zero, empty or false.
//
// # What this package does not do
//
// It does not decide which datoms belong in one run, what a run is keyed by,
// where the bytes go, or what order they are stored in. It does not compress,
// encrypt or checksum: the block carrying a run is checksummed by ADR-005 and
// verified on read by ADR-024, and a second mechanism would be a second answer
// to one question.
//
// See docs/adr/ADR-025-datom-encoding.md.
package datom
