# Task ADR-023-T2: Write a link, and walk one, from the language

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ql.RefMarker`, `ql.Write.ValueIsReference`, `ql.Traverse`, `ql.ErrNoDepth`, `ports.Datom.IsReference`, `session.Result.Reached`
**Consumes:** `link.Walk`, `link.Path`, `link.Resolver` (T1); `ql.Write` from ADR-022; `temporal.Visible` from ADR-002; `session.Session` from ADR-022
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a traversal carrying one time clause for the whole walk`, `a reference literal being distinguishable from a string with the same characters`, `only references being followed`, `a traversal refusing an unbounded depth`

⚠ **This task modifies `internal/core/ql/**` (ADR-011), `internal/core/ports/**`
(ADR-003) and `internal/core/session/**` (ADR-022).** The `ports.Datom` change is
one field: ADR-023 rule 2 says a link is an ordinary datom, so the kind lives on
the datom rather than in a second structure.

★ **An earlier version of this task was `pending` on the storage engine, and that
was wrong.** A reference literal and a `TRAVERSE` statement are pure parsing, and
the session already holds datoms to walk. The same over-deferral was made for
`SEARCH` and corrected the same way: "this waits on the storage engine" is true
of persistence and rarely true of meaning.

## Goal

Let a caller say "this refers to that", and ask what a hierarchy looked like at
an instant, in the language rather than in Go.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | `RefMarker`, and the `TRAVERSE`/`DEPTH` keywords. |
| `internal/core/ql/write.go` | modify | A reference value, and a shared tail so the two value paths cannot diverge. |
| `internal/core/ql/traverse.go` | add | `Traverse`, its parser, and `ErrNoDepth`. |
| `internal/core/ql/parse.go` | modify | Dispatch `TRAVERSE`. |
| `internal/core/ports/ports.go` | modify | `Datom.IsReference` — the kind, stored on the datom. |
| `internal/core/session/session.go` | modify | Execute a traversal, and resolve links from datoms. |
| `internal/core/ql/traverse_test.go`, `internal/core/session/session_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md` | modify | The literal, the statement, and why there is no per-hop clause. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTraverseCarriesOneTimeClause`, `TestTraverseRequiresADepth`, `TestAReferenceLiteralIsNotAString`, `TestTraverseWalksLinksAtOneInstant`, `TestOnlyReferencesAreFollowed`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `->` as a reference marker in the lexer. ★It is unambiguous: a bare `-` only starts a number when a digit follows, and a quoted value is a different token kind — so `->x` is a link and `'->x'` is the text. [proof: mutation]
3. [S3] Parse a reference value on both write verbs, sharing one tail with the literal path so a reference cannot quietly accept a `TRANSACTION` clause the literal path refuses. [proof: mutation]
4. [S4] Add `TRAVERSE <entity> DEPTH <n>` with ONE time clause for the whole walk. ⚠There must be no per-hop qualifier: a shape query has one per leg and the symmetry is tempting, but here it would let a caller ASK for a tree assembled from several instants — making ADR-023's central defect a feature rather than a bug. [proof: mutation]
5. [S5] Require the depth in the grammar, matching `link.Walk`'s refusal rather than restating it. [proof: mutation]
6. [S6] Carry the kind onto `ports.Datom` and execute a traversal in the session, resolving links through `temporal.Visible` at ONE snapshot. [proof: mutation]
7. [S7] Do not index a reference as full text. ⚠Its bytes are an entity name, not prose; matching them would answer "what links to this" with something that only looks like an answer, and inbound edges are a separate index and a deferred decision.
8. [S8] Document the literal, the statement and the absent per-hop clause. ★The coverage gate fails until `Traverse`, `ErrNoDepth` and `RefMarker` appear in the guide. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... ./internal/core/session/... -race -run 'TestTraverseCarriesOneTimeClause|TestTraverseRequiresADepth|TestAReferenceLiteralIsNotAString|TestTraverseWalksLinksAtOneInstant|TestOnlyReferencesAreFollowed|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr023-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr023-t2a.out \
  && go test ./internal/core/ql/... ./internal/core/session/... ./internal/core/link/... ./internal/core/ports/... -race -count=1 2>&1 | tee /tmp/adr023-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr023-t2b.out \
  && go run ./cmd/sdev1-ql --clock 1000 --statements "ASSERT a link = ->b" --statements "TRAVERSE a DEPTH 1" 2>&1 | tee /tmp/adr023-t2c.out \
  && grep -q "b (depth 1)" /tmp/adr023-t2c.out
```

The last segment RUNS the binary and greps for the walked entity, because a
traversal that compiles and returns nothing would otherwise pass.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTraverseCarriesOneTimeClause` | `internal/core/ql/traverse_test.go` | One clause attaches to the whole walk and resolves through the same table; a per-hop qualifier does NOT parse, so the tree that never existed cannot be requested | — | S4 |
| `TestTraverseRequiresADepth` | `internal/core/ql/traverse_test.go` | A missing, zero, negative or non-numeric depth is refused by name rather than defaulted to unbounded | — | S5 |
| `TestAReferenceLiteralIsNotAString` | `internal/core/ql/traverse_test.go` | `->star-1` and `'star-1'` carry the SAME characters and parse to different kinds; a reference also works on `RETRACT`, keeps a validity clause, and still refuses a `TRANSACTION` clause through the shared tail | — | S2, S3 |
| `TestTraverseWalksLinksAtOneInstant` | `internal/core/session/session_test.go` | Against a graph RESHAPED between two instants, a walk returns the shape that held at the instant asked for and never a mixture; the depth bound applies through the language | — | S4, S6 |
| `TestOnlyReferencesAreFollowed` | `internal/core/session/session_test.go` | A literal spelling an entity name is not an edge, and a retracted link stops being followed while remaining a datom | — | S6, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Parse` dispatches `TRAVERSE`, `ASSERT` accepts the marker, and the session is the only thing that turns either into datoms and a walk. |
| 3 — the caller can discover it | Both are in `docs/QUERY-LANGUAGE.md`, and the coverage gate fails until they are. |
| 4 — it is used | `cmd/sdev1-ql` runs a traversal, and the fence executes it. |

## Mutation Log

- 2026-09-04 · 6a2cef7* · mutant killed · exit 1 · `internal/core/ql/traverse.go` · parses the time clause and discards it, so every traversal silently walks the graph as it stands NOW however the caller qualified it. The statement still parses and the qualifier still validates, so a historical hierarchy query returns today shape with nothing indicating it · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · covers:a traversal carrying one time clause for the whole walk
- 2026-09-04 · 6a2cef7* · mutant killed · exit 1 · `internal/core/ql/write.go` · stops recognising the reference marker, so a link and a string carrying the same characters become the same value. Every edge in the graph disappears while both statements still parse, which is what inferring a reference from its bytes amounts to in reverse · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · covers:a reference literal being distinguishable from a string with the same characters
- 2026-09-04 · 6a2cef7* · mutant killed · exit 1 · `internal/core/session/session.go` · follows every asserted value as if it were a link, which is exactly what inferring an edge from the bytes produces: a note whose text happens to spell an entity name becomes an edge, and the graph changes whenever unrelated data does · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · covers:only references being followed
- 2026-09-04 · 6a2cef7* · mutant killed · exit 1 · `internal/core/ql/traverse.go` · makes DEPTH optional with an effectively unbounded default, which reads as a convenience. A caller who omits it then walks a graph they do not control to its full extent, which is the scan the required bound exists to prevent · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · covers:a traversal refusing an unbounded depth

## Invariants

- A traversal carries exactly one time clause; a per-hop qualifier does not parse.
- A reference literal is not a string with the same characters.
- Only references are followed; literals and retracted links are not.
- The depth is required in the grammar as well as in the walk.

## Risks

- ⚠ **A per-hop time qualifier is the tempting symmetry**, because a shape query has one per leg and it is genuinely right there. Here it would let a caller ASK for a tree assembled from several instants, turning ADR-023's central defect into a documented feature. The test asserts it does not parse rather than assuming there is nowhere to put it.
- ⚠ **A reference test using DIFFERENT text from the string it is compared with proves nothing.** Both spell `star-1`, so a parser that ignored the marker entirely would fail.
- ⚠ **Two value paths invite divergence.** The reference path returns early, so it could easily have skipped the `TRANSACTION` refusal that the literal path applies. They share one tail, and the test checks a reference write still refuses that clause.
- **A reference is not indexed as text.** That is a decision rather than an omission: matching an entity name as prose would answer "what links to this" with something that only resembles an answer, and inbound edges are their own index (`BACKLOG.md` §29).
- The session resolves links from its own datoms, so nothing here proves a real storage layer will pass ONE snapshot through a walk rather than taking a fresh read per hop. ADR-023 carries that as a follow-up.

## Stop Condition

Stop and ask before adding a per-hop time qualifier to a traversal, however
naturally it follows from shape queries. It makes a tree that never existed
something a caller can request on purpose.

## Out of Scope

- Durable storage for links (deferred: `docs/adr/BACKLOG.md` §12)
- Inbound edges — "what points at this" (deferred: `docs/adr/BACKLOG.md` §29)
- A depth default for the language (deferred: `docs/adr/BACKLOG.md` §29)

## Verification Log
- 2026-09-04 · 6a2cef7* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · ms:4402
- 2026-09-04 · 6a2cef7* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · ms:4258
- 2026-09-04 · 6a2cef7* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · ms:4163
- 2026-09-04 · 6a2cef7* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · ms:4067
- 2026-09-04 · 6a2cef7* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a4d851cb3d7e5f4b96e6fb0454b4bfda877081bcb6d070e1da8d9260f2dac06 · ms:4276
