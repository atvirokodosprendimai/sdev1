# Task ADR-010-T1: The cursor, the subscription, and the registry a purge must reach

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `subscribe.Cursor`, `subscribe.Subscription`, `subscribe.Registry`, `subscribe.NewRegistry`, `subscribe.Registry.Register`, `subscribe.Registry.Sinks`, `subscribe.Subscription.Deliver`, `subscribe.ErrUnknownSink`, `subscribe.ErrDuplicateSink`
**Consumes:** `tail.Tail` and `tail.Watermark` from ADR-017, `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a cursor advancing only past acknowledged entries`, `a cursor being a transaction identifier rather than a position`, `registration being what makes a sink reachable by a purge`

## Goal

Let a consumer follow the log from where it left off, and make the set of
consumers something a purge can enumerate.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/subscribe/doc.go` | add | Package comment: the three verbs and which one is erasure, why a purge has three outcomes, and how following fails and resumes. |
| `internal/core/subscribe/subscribe.go` | add | `Cursor`, `Subscription`, `Registry`, delivery, and the two sentinels. |
| `internal/core/subscribe/subscribe_test.go` | add | The tests below. |

★ A subscription is a cursor over ADR-017's watermark and nothing more. The tail
already gives a stable prefix that cannot be half-observed, so following it needs
no new consistency mechanism — only a position and a rule about when it moves.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestCursorAdvancesOnlyPastAcknowledged`, `TestCrashedSinkResumesWithoutSkipping`, `TestCursorIsATransactionIdentifier`, `TestUnregisteredSinkIsUnreachable`, `TestDuplicateRegistrationIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Cursor` as a `tx.TxID` and a flag for "nothing consumed yet". ★It is an identifier rather than an offset so it survives compaction and renumbering, and so two subscribers' positions are comparable with everything else the system orders by.
3. [S3] Define `Subscription`: a sink name and its cursor.
4. [S4] Implement `Deliver`: walk the tail from the cursor to a watermark, hand entries to the sink, and advance ONLY past what the sink acknowledged. ★A cursor that advanced past an unacknowledged entry would let a crashed sink resume beyond what it processed, and a backup missing entries looks exactly like a complete one.
5. [S5] Make delivery at-least-once and say so. ★Exactly-once needs the sink's own writes to be transactional with its cursor advance, which is the sink's property; claiming it here would be claiming something this layer cannot deliver.
6. [S6] Implement `Registry`: register a sink by name, refuse a duplicate, and list every registered sink. ★Registration is the act that makes a sink reachable by a purge, and T2's fan-out enumerates exactly this list.
7. [S7] Write the package comment naming mark, shred and sweep as three mechanisms and stating which one is erasure. [proof: human: a reader confirms the comment says an operator who MARKS and reports the data erased has said something false, rather than merely listing three verbs]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/subscribe/... -race -run 'TestCursorAdvances|TestCrashedSink|TestCursorIsATransaction|TestUnregisteredSink|TestDuplicateRegistration' -count=1 2>&1 | tee /tmp/adr010-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr010-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestCursorAdvancesOnlyPastAcknowledged` | `internal/core/subscribe/subscribe_test.go` | A sink that acknowledges some entries and refuses the rest leaves the cursor at the last acknowledged one, so nothing unprocessed is ever behind it | — | S3, S4 |
| `TestCrashedSinkResumesWithoutSkipping` | `internal/core/subscribe/subscribe_test.go` | A sink that fails mid-stream and is redelivered sees every entry it had not acknowledged, and the union of what it saw covers the whole range with no gap | — | S4, S5 |
| `TestCursorIsATransactionIdentifier` | `internal/core/subscribe/subscribe_test.go` | A cursor is expressed as a `tx.TxID` and compares against the same order the rest of the system uses, so it is meaningful after positions change | — | S2 |
| `TestUnregisteredSinkIsUnreachable` | `internal/core/subscribe/subscribe_test.go` | A sink that was never registered does not appear in the list a purge enumerates, and delivering to one is refused by name | — | S6 |
| `TestDuplicateRegistrationIsRefused` | `internal/core/subscribe/subscribe_test.go` | Registering one name twice is refused rather than silently replacing, so a second sink cannot inherit the first's cursor and skip everything before it | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Registry.Sinks` is what T2's purge enumerates, so an unregistered sink is invisible by construction; deleting registration breaks `TestUnregisteredSinkIsUnreachable`. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; `Deliver` returning the advanced cursor states that the caller owns the position. |
| 4 — it is used | Nothing measures this yet; no sink and no transport exist. |

## Mutation Log

- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/subscribe.go` · advances the cursor to everything delivered rather than to what the sink acknowledged, so a sink that crashes mid-stream resumes past what it processed and its backup is silently short — which looks exactly like a complete one · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · covers:a cursor advancing only past acknowledged entries
- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/subscribe.go` · treats the cursor own entry as undelivered so every resume redelivers the last acknowledged transaction, which is only detectable because the position is an ordered identifier rather than an opaque offset · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · covers:a cursor being a transaction identifier rather than a position
- 2026-09-04 · 60fc258* · mutant killed · exit 1 · `internal/core/subscribe/subscribe.go` · lets a second sink register under an existing name and inherit its cursor, so the newcomer skips everything before it while a purge still sees one entry and reports on one sink · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · covers:registration being what makes a sink reachable by a purge

## Invariants

- A cursor advances only past entries the sink acknowledged.
- A cursor is a transaction identifier, never a position or an offset.
- Delivery is at-least-once; a sink must tolerate a repeat.
- A sink is reachable by a purge if and only if it is registered.
- This package performs no network or file I/O.

## Risks

- ⚠ A resume test that redelivers from the START would pass even for a cursor that skips, because the sink would see everything anyway. `TestCrashedSinkResumesWithoutSkipping` redelivers from the CURSOR and asserts the union of both passes covers the range exactly once or more — never with a gap.
- "Advances only past acknowledged" is easy to test with a sink that acknowledges everything, which proves nothing. The test uses a sink that acknowledges a prefix and then refuses, so the cursor's stopping point is the assertion.

## Stop Condition

Stop and ask if a sink wants exactly-once delivery. It cannot be provided here,
and the honest answer changes what the sink must do rather than what this
package does — so it is a design conversation, not an implementation detail.

## Out of Scope

- The three verbs and the purge fan-out — that is T2.
- Delivering to a remote sink over a network (deferred: `docs/adr/BACKLOG.md` §18)
- Reclaiming space (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1644
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1718
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1658
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1670
- 2026-09-04 · 60fc258* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1830
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:340ba7f8aff5e8eb50b4823faa28fe17e353dc0c08bbb83c31b534dd6d4fbc81 · ms:1798
