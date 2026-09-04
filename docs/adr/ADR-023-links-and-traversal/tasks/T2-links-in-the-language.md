# Task ADR-023-T2: Write a link, and walk one, from the language

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ql` reference literals, `ql.Traverse`
**Consumes:** `link.Ref`, `link.Walk`, `link.Kind` (T1); `ql.Write` from ADR-022; a storage engine (`docs/adr/BACKLOG.md` §12)
**Data dependency:** needs a store to walk — a traversal over one entity's links is a read per hop, and there is nothing to read from
**Proof map:** v1
**Rests-on:** `a traversal statement carrying one time clause for the whole walk`, `a reference literal being distinguishable from a string in the grammar`

## Status

⚠ **`pending`, and blocked on the storage engine** (`BACKLOG.md` §12). A
traversal is a read per hop per level; the in-memory session holds one entity's
datoms and has no notion of following a reference across entities at scale.

★ **This is not the same as ADR-023 being unfinished.** What a reference IS, and
what walking one MEANS — including the rule that every hop resolves at one
instant — are settled by T1 and proved by mutation with no storage anywhere.

## Goal

Let a caller say "this refers to that", and ask what a hierarchy looked like at
an instant, in the language rather than in Go.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | A reference literal form, distinct from a string. |
| `internal/core/ql/traverse.go` | add | `Traverse`: a bounded walk with one time clause. |
| `internal/core/ql/traverse_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md` | modify | The literal, the statement, and the same-instant rule. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTraverseCarriesOneTimeClause`, `TestAReferenceLiteralIsNotAString`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Add a reference literal to the grammar, distinct from a quoted string, so `ASSERT` can state a link. [proof: mutation]
3. [S3] Add `TRAVERSE … DEPTH n` with ONE time clause for the whole walk. ⚠There must be no per-hop qualifier. A per-leg clause is right for a shape query, and here it would make the tree-that-never-existed SAYABLE — which is worse than it merely being implementable, because then it is a feature rather than a bug. [proof: mutation]
4. [S4] Require the depth, matching T1's refusal rather than restating it. [proof: mutation]
5. [S5] Document the literal, the statement, and why there is no per-hop clause. ★The coverage gate fails until the new exports appear in the guide. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestTraverseCarriesOneTimeClause|TestAReferenceLiteralIsNotAString|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr023-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr023-t2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTraverseCarriesOneTimeClause` | `internal/core/ql/traverse_test.go` | A traversal takes one clause for the whole walk, and a per-hop qualifier does not parse — so the tree that never existed cannot be requested | — | S3 |
| `TestAReferenceLiteralIsNotAString` | `internal/core/ql/traverse_test.go` | A reference literal and a quoted string with the same characters parse to different value kinds | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The two tests above. |
| 2 — something selects it | `Parse` dispatches `TRAVERSE`, and `ASSERT` accepts the reference literal. |
| 3 — the caller can discover it | Both are in `docs/QUERY-LANGUAGE.md`, and the coverage gate fails until they are. |
| 4 — it is used | `pending` — blocked on the storage engine. |

## Mutation Log

## Invariants

- A traversal carries exactly one time clause.
- A reference literal is not a string.
- The depth is required in the grammar as well as in the walk.

## Risks

- ⚠ **A per-hop time qualifier is the tempting symmetry**, because a shape query has one per leg and it is genuinely right there. Here it would let a caller ASK for a tree assembled from several instants, turning ADR-023's central defect into a documented feature.
- ⚠ **A reference literal that is just a string with a marker character** invites a value that legitimately starts with that character to become an accidental edge. Whatever form is chosen must be unambiguous for arbitrary content.
- The traversal returns entities, not a shaped tree. What a caller does with the reachable set is theirs, and inventing a nesting format here would be a second result model.

## Stop Condition

Stop and ask before adding a per-hop time qualifier to a traversal, however
naturally it follows from shape queries. It makes a tree that never existed
something a caller can request on purpose.

## Out of Scope

- The storage engine (deferred: `docs/adr/BACKLOG.md` §12)
- Inbound edges (deferred: `docs/adr/BACKLOG.md` §29)

## Verification Log
