# Task ADR-044-T1: Fire ADR-003's falsifier at real registry records

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** nothing — this task adds a test and no exported surface
**Consumes:** `command.Transaction`, `command.ErrCrossEntity` from ADR-003; `ports.Datom`, `temporal.Interval` from ADR-002/003; `eval.Read`, `ports.Inbound` from ADR-035; `link` references from ADR-023
**Data dependency:** ⚠ **A REAL CORPUS was read to design this** — `juridiniai.jsonl`, 548,547 Lithuanian public-procurement legal entities, 178 MB, examined 2026-09-05. The TEST is hermetic: real records are copied in as fixtures so it runs without the file, which is git-ignored and never committed.
**Proof map:** v1
**Rests-on:** `a real multi-entity legal act committing as one transaction`, `the act's participants being readable through the inbound index`, `the denormalised shape agreeing on the valid axis despite two transactions`

## Goal

Fire ADR-003's own falsifier — *"a legitimate domain operation cannot be expressed
within one entity"* — at a domain that really contains multi-entity operations,
and record what the boundary costs rather than only whether it survived.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/command/domain_test.go` | add | The tests below, over real records copied from the corpus. |
| `internal/core/command/doc.go` | modify | What the boundary cost when it met a real registry, and the one property that would break it. |
| `.gitignore` | modify | `*.jsonl`, so a 178 MB third-party crawl in the working tree cannot be committed. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARealMultiEntityActFitsTheBoundary`, `TestTheActsParticipantsAreReadable`, `TestTheDenormalisedShapeAgreesOnTheValidAxis`, `TestARegistryRecordIsOneEntity`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Copy REAL records from the corpus as fixtures — including the entities whose `legalStatus` is `Dalyvaujantis reorganizavime` and `Reorganizuojamas`. ★Real ones, because the value of this test is that the DOMAIN produced the multi-entity case; inventing one would prove only that the author can imagine it. [proof: acceptance]
3. [S3] Show a registry record is one entity: its twelve attributes commit in one `Transaction`, and adding a second entity's datom is `ErrCrossEntity`. [proof: mutation]
4. [S4] Model the reorganisation AS AN ENTITY, with `->participant` references to the companies, and commit it in ONE transaction. ★This is the answer: the act has a date, a kind and participants, so it is a thing, and registering a thing is a single-entity write. [proof: mutation]
5. [S5] Read the participants back through the inbound index. ⚠This is what makes the model usable rather than merely storable — and it means ADR-003's liveability depends on ADR-035, a dependency nobody planned. [proof: mutation]
6. [S6] Model the registry's OWN denormalised shape — `legalStatus` on each participant — as two transactions sharing one `Valid.From`, and show a read on the VALID axis sees both. ★Bitemporality is what pays for the atomicity the boundary does not provide. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/command/... -race -run 'TestARealMultiEntityActFitsTheBoundary|TestTheActsParticipantsAreReadable|TestTheDenormalisedShapeAgreesOnTheValidAxis|TestARegistryRecordIsOneEntity' -count=1 2>&1 | tee /tmp/adr044-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr044-t1a.out \
  && go test ./internal/core/command/... ./internal/core/eval/... ./internal/core/leafstore/... -race -count=1 2>&1 | tee /tmp/adr044-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr044-t1b.out \
  && ! git add -A --dry-run 2>&1 | grep -qi '\.jsonl'
```

⚠ The last segment is the corpus guard, and it asks the question that actually
matters: would `git add -A` stage it? An earlier version consulted
`git check-ignore`, which answered inconsistently in this checkout — and a guard
that reports "safe" for the wrong reason is worse than none when what it is
guarding is 178 MB of somebody else's crawl entering the history permanently.

The second command carries `eval` and `leafstore` because the participants are
read back through ADR-035's inbound path against a real leaf — the model is only
confirmed if the query works where the data actually lives.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARealMultiEntityActFitsTheBoundary` | `internal/core/command/domain_test.go` | **The falsifier ADR-003 names and ADR-044 fires.** A reorganisation involving three REAL companies — one `Reorganizuojamas`, two `Dalyvaujantis reorganizavime` — commits as ONE transaction on the act entity, with the participants as references. ⚠ And the cross-entity attempt is shown refused in the same test, so the boundary is visibly still in force rather than circumvented | — | S4 |
| `TestTheActsParticipantsAreReadable` | `internal/core/command/domain_test.go` | `READ ->name FROM [reorg-…]` over a real leaf returns exactly the participants. ★ Without this the normalised model is storable and unqueryable, which is why ADR-003's liveability depends on ADR-035 | — | S5 |
| `TestTheDenormalisedShapeAgreesOnTheValidAxis` | `internal/core/command/domain_test.go` | The registry's own shape — status on each participant — written as TWO transactions sharing the act's real date: a read `AS OF` that date sees both statuses, and a read before it sees neither. ⚠ The point is the shared `Valid.From`: with different ones the two facts disagree on the valid axis too and bitemporality stops covering the gap | — | S6 |
| `TestARegistryRecordIsOneEntity` | `internal/core/command/domain_test.go` | A real record's twelve attributes — five always-present, seven sparse — commit in one transaction, and a datom naming a second entity is `ErrCrossEntity` | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over records copied from a real corpus. |
| 2 — something selects it | `command.Transaction` is the only way a write is assembled, and it refuses a second entity at `Assert`. |
| 3 — the caller can discover it | `ErrCrossEntity` is named, and its comment already anticipated this case. |
| 4 — it is used | ★ This is the one record in the corpus whose rung 4 is a real domain rather than a deferral: the modelling is exercised against 548,547 real entities' shape, and the multi-entity case is one the registry produced rather than one this project imagined. |

## Mutation Log

- 2026-09-05 · 07642b1* · mutant killed · exit 1 · `internal/core/command/command.go` · removes the one-entity boundary entirely, so a legal act and its participants' own statuses can be written in a single transaction — which is what makes ADR-003's constraint look unnecessary, and it is the constraint that removes distributed commit from the whole system · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · covers:a real multi-entity legal act committing as one transaction
- 2026-09-05 · 07642b1* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · makes the inbound read return nothing, so a legal act's participants become unreadable — the normalised model is then storable and unqueryable, and ADR-003's liveability, which rests on this read, goes with it · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · covers:the act's participants being readable through the inbound index
- 2026-09-05 · 07642b1* · mutant inconclusive · exit 1 · `internal/core/temporal/temporal.go` · probe: does the fence reach the visibility predicate at all · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · covers:the denormalised shape agreeing on the valid axis despite two transactions
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · 07642b1* · mutant killed · exit 1 · `internal/core/temporal/temporal.go` · conflates the two axes: a business instant is tested against the datom's TRANSACTION time instead of its validity interval. The two facts of one legal act then stop agreeing at the act's date — the participant written a second later on the transaction axis becomes invisible — and the bitemporality that pays for the missing cross-entity atomicity is gone · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · covers:the denormalised shape agreeing on the valid axis despite two transactions

## Invariants

- A registry record is one entity.
- A multi-entity act is an entity, and registering it is one transaction.
- The act's participants are readable through the inbound index.
- The denormalised shape shares one valid-from.

## Risks

- ⚠ **Inventing the multi-entity case would prove nothing.** The value is that the DOMAIN produced it — `Dalyvaujantis` is a word the registry needed and this project did not predict. The fixtures are real records, and the record cites the corpus by shape, size and date.
- ⚠ **A test that only shows the act committing is half the answer.** The cross-entity attempt must be refused IN THE SAME TEST, or the boundary might simply have been circumvented rather than satisfied.
- ⚠ **The denormalised test must assert the SHARED valid-from.** Two writes with different real-world dates disagree on the valid axis as well as the transaction axis, and then bitemporality is not covering anything — it is just two unrelated facts.
- ⚠ **The corpus must not be committed.** 178 MB of a third-party crawl, and a `git add -A` would have taken it before `*.jsonl` was ignored. The fence checks it stays uncommittable.
- ⚠ **One domain is one domain.** This registry maintains no conserved quantity, so it cannot test ADR-044 rule 5's class at all. The record says so; the test cannot.
- ⚠ **AND THE CORPUS FOUND A SECOND DEFECT THE BOUNDARY WORK WAS NOT LOOKING FOR.** Every registry identifier is all-numeric — `111756039` — and the first version of `TestTheDenormalisedShapeAgreesOnTheValidAxis` failed to PARSE: `found number "111756039", expected an entity name`. ★ The escape already existed (ADR-021's backticks, added so a keyword could remain an attribute name) and covers it. Recorded because it was found by pointing the language at real data rather than by reading the grammar — a domain whose primary keys are integers is entirely ordinary, and nothing in the guide suggests quoting is for anything but keywords.

## Stop Condition

Stop and ask before relaxing `ErrCrossEntity` to model this domain. It is not
needed here — the act is an entity — and relaxing it is the one change that
reintroduces distributed commit to the whole system.

## Out of Scope

- A domain with a cross-entity invariant (deferred: `docs/adr/BACKLOG.md` §8 — the class ADR-044 rule 5 names)
- Procurement CONTRACTS, whose participants are asymmetric (deferred: `docs/adr/BACKLOG.md` §8 — a separate corpus)
- Ingesting 548,547 entities and measuring the cost (deferred: `docs/adr/BACKLOG.md` §12)
- Committing the corpus (permanent: boundary: a record cites a crawl's shape, size and date, never its bytes)

## Verification Log
- 2026-09-05 · 07642b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · ms:4133
- 2026-09-05 · 07642b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · ms:4144
- 2026-09-05 · 07642b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · ms:4151
- 2026-09-05 · 07642b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · ms:4067
- 2026-09-05 · 07642b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:2290deb4f8b2c630ee2e7f3054019190aaaa594a17199c5a460d7100f7cfcd39 · ms:4105
