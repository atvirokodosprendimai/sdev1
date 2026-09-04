# Task ADR-026-T2: Make a fact survive a restart, from the language

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `session.Open`, `session.Session.Seal`, `session.Session.Close`, the `--dir` flag on `cmd/sdev1-ql`
**Consumes:** `leafstore.Store`, `leafstore.Open`, `leafstore.Store.Entities`, `leafstore.Store.History`, `leafstore.Store.Append`, `leafstore.Store.Seal` (T1)
**Data dependency:** hermetic — a temporary directory, and the built binary run twice against it
**Proof map:** v1
**Rests-on:** `a fact written by one process being read by the next`, `rehydration restoring everything a session answers from rather than only its datoms`, `the built binary actually running rather than only compiling`

## Goal

Close `BACKLOG.md` §28 where a caller can see it: write a fact with one run of the
binary, read it back with the next.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/session/session.go` | modify | `Open` rehydrates from a store; writes reach its tail; `Seal`/`Close`. |
| `internal/core/session/doc.go` | modify | The package comment says the session is not a storage engine; that is now conditional and must say so. |
| `internal/core/session/session_test.go` | modify | The tests below. |
| `cmd/sdev1-ql/main.go` | modify | `--dir` opens a leaf; the session is sealed on the way out. |

⚠ `internal/core/session/**` and `cmd/sdev1-ql/**` are governed by ADR-022, not by
this record. This task adds a durable backing to what ADR-022 built and changes no
rule it stated — and the Acceptance re-runs ADR-022's own suites for that reason.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestASessionRehydratesFromItsStore`, `TestRehydrationRestoresTheSearchIndexToo`, `TestAWriteReachesTheStore`, `TestASessionWithNoStoreIsUnchanged`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `session.Open(tenant, now, store)`, which rehydrates by walking `store.Entities()` and `store.History(entity)`. ⚠`History` and not `Load`: rehydration wants every datom, and no single snapshot returns all of history — an instant on the business axis selects the facts true AT it. [proof: mutation]
3. [S3] Feed everything rehydration produces through the SAME path a live write takes, so the search index, the link resolver and the datom map are populated by one piece of code rather than three. ⚠A rehydration that restored only the datoms would leave `SEARCH` silently answering nothing after a restart. [proof: mutation]
4. [S4] Make a write append to the store's tail as well as the session's own state, and leave a session with no store behaving exactly as before. [proof: mutation]
5. [S5] Add `Seal` and `Close`, sealing the store's tail; both are no-ops without a store, so a caller need not ask which kind of session it holds.
6. [S6] Add `--dir` to `cmd/sdev1-ql`: open a leaf there, and seal on the way out. ⚠Seal AFTER the statements have run and before the process exits, or the run reports success and writes nothing. [proof: acceptance]
7. [S7] Update the session's package comment. It currently says the session is not a storage engine; with a store attached that is no longer the whole truth, and a comment that is half true is worse than one that is wrong. [proof: human: whether a package comment describes what the package now does is a judgement about prose, and a test that could make it would be asserting the comment against itself]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/session/... -race -run 'TestASessionRehydratesFromItsStore|TestRehydrationRestoresTheSearchIndexToo|TestAWriteReachesTheStore|TestASessionWithNoStoreIsUnchanged' -count=1 2>&1 | tee /tmp/adr026-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr026-t2a.out \
  && go test ./internal/core/session/... ./internal/core/ql/... ./internal/core/leafstore/... -race -count=1 2>&1 | tee /tmp/adr026-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr026-t2b.out \
  && D=$(mktemp -d) && go build -o "$D/sdev1-ql" ./cmd/sdev1-ql \
  && "$D/sdev1-ql" --dir "$D/leaf" --tenant 1 --clock 1000 --statements 'ASSERT planet-3 mass = "5.97e24"' > "$D/first.out" 2>&1 \
  && "$D/sdev1-ql" --dir "$D/leaf" --tenant 1 --clock 5000 --statements 'SELECT * FROM planet-3' > "$D/second.out" 2>&1 \
  && grep -q '5.97e24' "$D/second.out" \
  && rm -rf "$D"
```

⚠ The last `grep` is the point, not the exit codes. The second process must PRINT
the value the first one wrote — a binary whose `main` did nothing would satisfy
every exit status above it, which is exactly how ADR-001 T4 passed for a day while
being broken.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestASessionRehydratesFromItsStore` | `internal/core/session/session_test.go` | A session opened on a store that already holds sealed facts answers `SELECT` about them, including a value written before the process started | — | S2 |
| `TestRehydrationRestoresTheSearchIndexToo` | `internal/core/session/session_test.go` | After rehydration a `SEARCH` finds a rehydrated fact and a `TRAVERSE` follows a rehydrated link — so a restart restores everything the session answers from, not only its datom map | — | S3 |
| `TestAWriteReachesTheStore` | `internal/core/session/session_test.go` | An `ASSERT` through a session with a store leaves the datom in the store's tail, and it is there after `Seal` in a segment | — | S4 |
| `TestASessionWithNoStoreIsUnchanged` | `internal/core/session/session_test.go` | A session built by `New` still runs every statement, and `Seal` and `Close` on it are no-ops rather than refusals | — | S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above, plus the binary run twice in the fence. |
| 2 — something selects it | `--dir` is parsed by `cmd/sdev1-ql` and reaches `session.Open`; without it the session is exactly what ADR-022 built. |
| 3 — the caller can discover it | `--dir` appears in the binary's help, and the query guide documents it. |
| 4 — it is used | The fence runs the binary twice and greps the second run's OUTPUT for what the first one wrote. |

## Mutation Log

## Invariants

- A session with no store behaves exactly as ADR-022 built it.
- Rehydration and a live write populate the session by one code path.
- Sealing happens after statements run and before the process exits.

## Risks

- ⚠ **Rehydrating the datom map and forgetting the search index is the defect this task is most likely to ship.** Everything looks right: `SELECT` works, the fact is there, the restart clearly worked — and `SEARCH` returns nothing with no error. The mitigation is structural rather than diligent: rehydration goes through the same path as a live write, so there is no second place to forget.
- ⚠ **A fence that only checks exit codes would pass against a `main` that does nothing.** The second run's output is grepped for the value the first wrote.
- ⚠ **Sealing on the way out has an ordering trap:** seal before the statements run and the run succeeds while writing nothing. The fence's second process is what catches it, because it is the only reader that was not there for the write.
- The whole leaf is loaded into memory at open, since the session answers search and traversal from what it holds. That is honest for a single-leaf CLI and does not scale; it is the same bound `BACKLOG.md` §20 carries for the evaluator.
- `--dir` makes `cmd/sdev1-ql` write to a real filesystem for the first time. The fence uses a temporary directory it removes.

## Stop Condition

Stop and ask before making the session read through the store on every statement
instead of rehydrating at open. It looks like the cleaner design and it is not
reachable yet: search, faceting and traversal need to enumerate what a leaf holds,
`ports.Reader` deliberately cannot, and inventing a wider read contract here would
decide `BACKLOG.md` §20 by accident.

## Out of Scope

- Reading through the store per statement rather than rehydrating at open (deferred: `docs/adr/BACKLOG.md` §20)
- When to seal automatically, rather than on the way out (deferred: `docs/adr/BACKLOG.md` §15)
- More than one leaf in one session (deferred: `docs/adr/BACKLOG.md` §18)
- A write tool on the agent surface (deferred: `docs/adr/BACKLOG.md` §25)
- Making the write durable at acknowledgement (permanent: boundary: ADR-020 fixed the commit point at N memory replicas, and this task must not move it as a side effect of adding a disk)

## Verification Log
