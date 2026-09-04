# ADR-023 Tasks

Implementation tasks for ADR-023: A link is a typed datom, and a traversal
resolves every hop at one instant. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A typed reference, and a walk that resolves at one instant | done | — | `go test ./internal/core/link/... -race -run 'TestEveryHopResolvesAtOneInstant\|TestAReferenceIsAStoredKind\|TestACycleIsReported\|TestAWalkRefusesAnUnboundedDepth\|TestAMissingRetractedAndErased\|TestWalkRespectsItsDepthBound'` then the temporal and ports suites |
| T2 | Write a link, and walk one, from the language | done | — | `go test ./internal/core/ql/... ./internal/core/session/... -race -run 'TestTraverseCarriesOneTimeClause\|TestTraverseRequiresADepth\|TestAReferenceLiteralIsNotAString\|TestTraverseWalksLinksAtOneInstant\|TestOnlyReferencesAreFollowed\|TestQueryLanguageDocIsComplete\|TestPublishedExamplesParse'` then the link and ports suites, then RUN `cmd/sdev1-ql` |

Status: `pending` | `partial` | `blocked` | `done`.

★ **Both tasks are done, and links work end to end** — `ASSERT a orbits = ->b`
writes one, `TRAVERSE a DEPTH 2 AS OF 150` walks it as the graph stood then.

⚠ **T2 was written as `pending` on the storage engine, and that was wrong.** A
reference literal and a `TRAVERSE` statement are pure parsing, and the session
already held datoms to walk. The identical over-deferral was made for `SEARCH`
and corrected the same way — recorded because it is easy to repeat: *"this waits
on the storage engine"* is true of persistence and rarely true of meaning.

What genuinely remains is durability (`BACKLOG.md` §12) and inbound edges (§29).

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `link.Kind`, `link.Value`, `link.Ref`, `link.Walk` | T2 | T1 before T2 |

## Notes

- ⚠ **A TRAVERSAL THAT RESOLVES EACH HOP AT ITS OWN INSTANT PRODUCES A TREE THAT
  NEVER EXISTED.** Ask for a hierarchy as it stood last March: the natural
  implementation reads the root at March and its children with a fresh read, at
  today's instant. Every node in the answer is real, every edge existed at some
  point, and the shape was never true at any moment. Nothing about it looks
  wrong — and it is wrong exactly where a bitemporal store is supposed to be
  trustworthy.
- **So `Walk` takes ONE snapshot and hands it to every hop**, and `Resolver`
  takes the snapshot as a parameter so a caller structurally cannot resolve a hop
  without saying when.
- **A link is a datom**, not an edge in a side table. It is bitemporal,
  retractable, one-entity-bound and inside the tenant subtree because it is not a
  new kind of thing. ⚠ A separate edge store would have to re-decide all four and
  would get at least one wrong.
- ★ **Taxonomies are therefore free.** A hierarchy is links, links are datoms,
  datoms are bitemporal — so "what did this hierarchy look like in March" is a
  traversal at an instant rather than a feature anybody built.
- ⚠ **The kind is STORED, never inferred.** `"planet-9"` as a name and as a link
  are the same nine bytes. Guessing from shape makes every identifier-looking
  string an accidental edge, and the guess changes when unrelated data does.
- ⚠ **A cycle is reported, never truncated** — a partial path reads exactly like
  a complete one. And cycles are real here: a hierarchy edited over time can
  contain a loop that exists only at instants BETWEEN two edits, visible in a
  historical query and in no current one.
- ⚠ **A missing, retracted and ERASED target are one answer.** Distinguishing
  them rebuilds the existence oracle crypto-shredding exists to remove — a caller
  could discover whether a subject was erased by walking to it.
- **A walk is depth-bounded and the bound is required**, because an unbounded
  walk over a graph the caller does not control is a scan they did not ask for.
