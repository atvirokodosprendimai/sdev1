# Task ADR-010-T2: Three verbs, three guarantees, and a purge that is not done until every sink says so

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `subscribe.Mark`, `subscribe.Shred`, `subscribe.Sweep`, `subscribe.PurgeResult`, `subscribe.PurgeState`, `subscribe.Horizon`, `subscribe.ErrNotErasure`
**Consumes:** `subscribe.Registry`, `subscribe.Subscription` (T1), `crypt.Keystore` and `crypt.ErrKeyDestroyed` from ADR-007
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an unacknowledged sink making a purge incomplete rather than complete`, `the three verbs having three different guarantees`, `only shredding making a subject unreadable`

## Goal

Make an operator who removes a subject learn WHICH of the three things they got,
and which sinks still hold it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/subscribe/purge.go` | add | The three verbs, `PurgeResult`, `PurgeState`, `Horizon`, and `ErrNotErasure`. |
| `internal/core/subscribe/purge_test.go` | add | The tests below, including the falsifier named in ADR-010's `Enforced-by:`. |

★ There is deliberately no `Delete`. A single verb would be answered by a
different mechanism depending on context and an operator would not know whether
they got invisibility, erasure, or a promise about space.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestPurgeIsIncompleteWhileASinkHasNotAcknowledged`, `TestPurgeNamesWhoHasAcknowledgedAndWhoHasNot`, `TestThreeVerbsGiveThreeGuarantees`, `TestOnlyShreddingMakesASubjectUnreadable`, `TestThereIsNoDeleteVerb`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `PurgeState` with exactly three values — done, incomplete, refused — and no fourth. ★Two would force an unacknowledged sink to be reported as one of them, and both readings are wrong: "done" is a lie that surfaces at the next restore, "failed" suggests nothing happened when the primary copy is already gone.
3. [S3] Implement `Mark`: make a subject invisible immediately, changing no bytes. Record that anyone holding the data still has it.
4. [S4] Implement `Shred`: destroy the key through ADR-007's keystore, and fan out to every registered sink. ★This is the only one of the three that is erasure.
5. [S5] Implement `Sweep`: reclaim eventually, bounded by a `Horizon`, and reaching neither a backup nor a coded stripe already written elsewhere. Return `ErrNotErasure` if a caller asks it to erase.
6. [S6] Make the fan-out collect a per-sink acknowledgement and return `PurgeResult` naming both lists. [proof: mutation]
7. [S7] Report INCOMPLETE when any registered sink has not acknowledged, never done and never refused. ★Incomplete is the only one of the three an operator can act on: it says the primary copy is gone AND names the sink to chase.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/subscribe/... -race -run 'TestPurgeIsIncomplete|TestPurgeNamesWho|TestThreeVerbs|TestOnlyShredding|TestThereIsNoDelete' -count=1 2>&1 | tee /tmp/adr010-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr010-t2a.out \
  && go test ./internal/core/subscribe/... ./internal/core/crypt/... ./internal/core/tail/... -race -count=1 2>&1 | tee /tmp/adr010-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr010-t2b.out
```

The first command is this task's own work and can carry the verdict alone; the
second is the regression half over T1's cursors, ADR-007's keystore and the tail
a subscription follows.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPurgeIsIncompleteWhileASinkHasNotAcknowledged` | `internal/core/subscribe/purge_test.go` | A registered sink that never acknowledges yields INCOMPLETE — not done, and not refused — so a restore can never resurrect what an operator was told was gone. **The falsifier ADR-010 names in `Enforced-by:`** | — | S6, S7 |
| `TestPurgeNamesWhoHasAcknowledgedAndWhoHasNot` | `internal/core/subscribe/purge_test.go` | The result lists both sets, so an operator has the one sink to chase rather than a verdict about all of them | — | S6 |
| `TestThreeVerbsGiveThreeGuarantees` | `internal/core/subscribe/purge_test.go` | Mark leaves the bytes readable, shred makes them unreadable, sweep does neither and is bounded by a horizon — the three are observably different | — | S3, S4, S5 |
| `TestOnlyShreddingMakesASubjectUnreadable` | `internal/core/subscribe/purge_test.go` | After a mark or a sweep the data still decrypts; after a shred it does not. Asking a sweep to erase yields `ErrNotErasure` | — | S4, S5 |
| `TestThereIsNoDeleteVerb` | `internal/core/subscribe/purge_test.go` | A reflective check over the package's exported surface finds no `Delete`, `Remove` or `Purge` verb that would collapse the three, so the distinction cannot be lost by a later convenience | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Registry.Sinks` from T1 is what the fan-out enumerates, so registration is what makes a sink reachable; deleting the acknowledgement collection breaks the falsifier. |
| 3 — the caller can discover it | Three separately named verbs and a three-valued result; a caller reading the package cannot fail to see that "delete" is not one thing. |
| 4 — it is used | The purge state an operator sees is the measurement, and it is meaningful from the first call. |

## Mutation Log

- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/purge.go` · reports every purge as done regardless of who acknowledged, which is the lie that surfaces months later as a restore resurrecting what an operator was told was gone — with nothing having reported anything in between · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · covers:an unacknowledged sink making a purge incomplete rather than complete
- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/purge.go` · makes a mark claim erasure, so an operator who made a subject invisible is told the bytes are gone when anyone holding them still has them — the single most common way a deletion is misreported · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · covers:the three verbs having three different guarantees
- 2026-09-04 · 60fc258* · mutant inconclusive · exit 1 · `internal/core/subscribe/purge.go` · reports a shred without destroying the key, so the subject stays fully readable while the operator is told it was erased and the audit trail records an erasure that did not happen · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · covers:only shredding making a subject unreadable
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 60fc258* · mutant inconclusive · exit 1 · `internal/core/subscribe/purge.go` · reports a shred without destroying the key, so the subject stays fully readable while the operator is told it was erased and an audit trail records an erasure that did not happen · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · covers:only shredding making a subject unreadable
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/purge.go` · reports a shred without destroying the key, so the subject stays fully readable while the operator is told it was erased and an audit trail records an erasure that did not happen · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · covers:only shredding making a subject unreadable

## Invariants

- An unacknowledged registered sink yields INCOMPLETE, never done and never refused.
- There are exactly three purge states and exactly three verbs.
- Only `Shred` makes a subject unreadable; `Mark` and `Sweep` never do.
- A sink is reached if and only if it is registered.
- No exported identifier collapses the three verbs into one.

## Risks

- ⚠ **A purge test whose sinks all acknowledge proves nothing about the dangerous case.** The falsifier registers a sink that NEVER acknowledges, which is what a silently-unwired backup looks like from here, and asserts the state is incomplete rather than done.
- ⚠ `TestThereIsNoDeleteVerb` reflects over the exported surface rather than checking a known list, because the failure mode is somebody ADDING a convenience wrapper later. A hand-written list passes when a name is added.
- A test asserting "mark leaves the data readable" can pass because the fixture was never readable. The three-verb test reads the subject successfully first, so each assertion is about the verb rather than about a broken fixture.

## Stop Condition

Stop and ask before adding a fourth purge state, or any single verb that means
"remove this". Both are reasonable-sounding requests and both destroy the
distinction this task exists to make: a fourth state is where "we are working on
it" hides, and a single verb is how a mark gets reported as an erasure.

## Out of Scope

- Delivering a purge to a remote sink (deferred: `docs/adr/BACKLOG.md` §18)
- Reclaiming actual space (deferred: `docs/adr/BACKLOG.md` §12)
- Escalating a purge that stays incomplete (deferred: ADR-012's console)
- Authorizing who may purge (deferred: `docs/adr/BACKLOG.md` §11)

## Verification Log
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3898
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3925
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3872
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3826
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3859
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:ec6900a709d59bacbd787ca255c9aac0ebd112878064974bc0907d29a4974236 · ms:3888
