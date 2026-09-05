# ADR-032 Tasks

Implementation tasks for ADR-032: a topology map carries a generation identified
by a transaction, and a placement against a map that cannot say which it is, is
refused. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

One task.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Give a map an identity, and refuse to place without one | done | — | `go test ./internal/core/placement/... -race -run '…three tests…'`, then `topology`, then every package that loads a map, then build `sdev1-addr` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `topology.Map.Generation` | whatever places segments (`BACKLOG.md` §6/§18) | none within this record |

## Notes

- ★ **Two things about `BACKLOG.md` §6 turned out differently from how it was
  written.** It says placement must become a function of `(leaf, map version)` and
  that this changes `placement.Resolve`'s signature. It does not: `Resolve` already
  TAKES the map, and never reads a global current one. What was missing is not a
  parameter but an IDENTITY — a `Map` could not say which map it was.
- ⚠ **And the field a reader would reach for was already taken.** `topology.Map`
  had a `Version` field meaning the FILE FORMAT — a constant this build compares
  against, which has never changed. Anyone implementing §6 would very reasonably
  put the map's identity there, every map in the cluster would claim the same
  generation forever, and nothing would fail. So the field is RENAMED to
  `FormatVersion`: the trap is removed rather than documented around.
- **The generation is a `tx.TxID`.** ADR-002's identifier is the only total order
  in this system; a counter would be a second clock, and a content hash does not
  order at all — "which map came first" would become unanswerable.
- ★ **A map may be LOADED without a generation; a PLACEMENT against one is
  refused.** Reading a map to inspect a cluster's shape is legitimate, and the
  refusal belongs where the consequence is: a placement you cannot reproduce is a
  segment you cannot find. ⚠ A zero generation must never read as "generation
  zero" — that is an answer, and it means every map is the same map.
- ⚠ **The generation is AUTHORED, never assigned at load.** One minted at load
  gives the same file a different identity in every process, which is the original
  failure wearing a new hat.
- ⚠ **A generation may not be retired while anything placed under it exists.**
  Retiring a map is data loss with extra steps — the segments placed under it
  become unlocatable — and nothing about it looks like deleting data. Every
  generation is therefore retained by default, so "we should prune old maps"
  arrives as a proposal against a written decision rather than as tidying.
- ⚠ **No segment records a generation yet, because nothing places one.** `Resolve`
  is called by a demonstration binary and by the prefetcher; no writer consults it.
  The header field is deferred to whatever wires placement to storage rather than
  guessed at now.
