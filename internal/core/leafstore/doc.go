// Package leafstore makes many segments answer as one leaf.
//
// A leaf is a directory. Every file in it is a complete segment, because ADR-024
// publishes one by renaming it into place — so the directory listing IS the
// manifest and there is nothing separate to keep in step with it.
//
// # The defect this package exists to prevent
//
// A leaf's history is spread across many segments, so a read has to merge them,
// and the obvious way to merge is in the order the files came back. A directory
// listing is sorted by name, so that reads as deterministic.
//
// ⚠ It is not. It makes the answer depend on what the files are CALLED. A rename
// reorders it. A copy that preserves only content reorders it. A restore that
// lays files down in a different order reorders it. None of those looks like a
// data-loss event, and the wrong answer is a plausible one — an older value
// winning over a newer one, with no error anywhere.
//
// So the merge orders by the datoms' own transaction identifiers, which ADR-002
// already made total, and ★ a segment's name is random and carries nothing. A
// name that sorted would be a name something could come to depend on.
//
// # Reading
//
// [Store.History] is the primitive: every datom the leaf holds for one entity.
// [Store.Load] is that filtered at a snapshot, and [Store.Attributes] is the
// present shape — the attributes whose latest visible datom is an assertion.
//
// ⚠ Rehydrating state needs History rather than Load, because no snapshot returns
// all of history: an instant on the business axis selects the facts true AT it.
//
// # Writing
//
// [Store.Append] adds to an in-memory tail and touches no disk. That is not an
// oversight: ADR-020 fixed the commit point at N memory replicas in distinct
// failure domains, so a segment is a durability tier and not the commit path.
// [Store.Seal] is explicit, and WHEN to call it is deliberately not decided here
// (docs/adr/BACKLOG.md §15).
//
// # What this package does not do
//
// It decides no policy: not when to seal, not when to compact, not what
// visibility means — that is ADR-002's single comparison site, called rather than
// reimplemented. A read touches every segment in the leaf, which is linear in
// seal count and is the cost that makes compaction matter.
//
// See docs/adr/ADR-026-leaf-store.md.
package leafstore
