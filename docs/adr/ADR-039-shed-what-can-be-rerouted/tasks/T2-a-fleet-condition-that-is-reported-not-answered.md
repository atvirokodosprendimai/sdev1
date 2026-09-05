# Task ADR-039-T2: A fleet condition that is reported, not answered

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S
**Owner:** unassigned
**Produces:** `admit.Fleet`, `admit.NewFleet`, `admit.Fleet.Observe`, `admit.Fleet.AllWithdrawn`, `admit.Fleet.Report`, `observe.KindFleetWithdrawn`
**Consumes:** `admit.State`, `admit.StateWithdrawn` from ADR-015; `watch.Obligation`, `watch.Ledger` from ADR-038; `observe.Kind` from ADR-012
**Data dependency:** hermetic — replica states are supplied
**Proof map:** v1
**Rests-on:** `an all-withdrawn fleet being reported rather than resolved`

## Goal

Make "every replica has withdrawn" visible without choosing what to do about it,
and keep the fleet's knowledge structurally out of reach of a node's own decision.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/admit/fleet.go` | add | `Fleet`, `AllWithdrawn`, and the obligation it raises. |
| `internal/core/observe/kinds.go` | modify | `KindFleetWithdrawn`, with its declared reader and fields. |
| `internal/core/admit/fleet_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnAllWithdrawnFleetIsAnObligation`, `TestTheFleetCannotChangeANodesOwnDecision`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Declare `observe.KindFleetWithdrawn` with a named reader and fields, as ADR-012 requires of every kind. [proof: mutation]
3. [S3] Implement `Fleet` as a view over replica STATES, supplied by name. It holds no `Controller` and no ceiling: it observes what nodes reported, and cannot reach into how they decided. ⚠This is STRUCTURAL and a mutant cannot falsify it — see the Risks note. [proof: human: the property is that a coupling does not exist. Falsifying it means ADDING one — a package-level channel from the fleet to `Decide`, or a method taking a `*Controller` — which is a redesign rather than a mutation of code that is there. `TestTheFleetCannotChangeANodesOwnDecision` builds a fleet reporting every peer withdrawn and shows a node's answer is unchanged in both directions, which is the strongest available check and is not a mutant]
4. [S4] Raise an ADR-038 obligation when every replica has withdrawn, and only then. ★A state, it matters, nobody has dealt with it — so the mechanism that already exists for that is the one used, rather than a second notion of "somebody should look". [proof: mutation]
5. [S5] ⚠Report and do NOT resolve: `Fleet` offers no floor, no override and no way to keep a node joined. `BACKLOG.md` §22 says the three candidate answers need a cluster to choose between, and rule 1 forbids the one that would reach the node's decision. [proof: human: the same absent-coupling property as S3 — there is no method to remove, because the point is that none was written]
6. [S6] Confirm the obligation clears only through `watch.Ledger.Acknowledge` — a fleet that recovers does NOT silently resolve it, because a cluster that shed everything and recovered is exactly the event somebody should still see. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/admit/... -race -run 'TestAnAllWithdrawnFleetIsAnObligation|TestTheFleetCannotChangeANodesOwnDecision' -count=1 2>&1 | tee /tmp/adr039-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr039-t2a.out \
  && go test ./internal/core/admit/... ./internal/core/observe/... ./internal/core/watch/... -race -count=1 2>&1 | tee /tmp/adr039-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr039-t2b.out
```

The second command carries `observe` and `watch` because the condition is a
declared kind and an obligation: a change to either that altered what is declared
or what clears an obligation would change what this reports, silently.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnAllWithdrawnFleetIsAnObligation` | `internal/core/admit/fleet_test.go` | With one replica still joined there is no obligation; with every replica withdrawn there is one, naming the replicas. ★ And it does NOT clear when a replica rejoins — only `watch.Ledger.Acknowledge` clears it, because a cluster that shed everything and recovered is exactly what somebody should still see | — | S4, S6 |
| `TestTheFleetCannotChangeANodesOwnDecision` | `internal/core/admit/fleet_test.go` | A `Controller` above its withdraw threshold reaches `StateWithdrawn` while a `Fleet` reports every peer withdrawn — the fleet is constructed, consulted, and changes nothing. ⚠ The fleet is deliberately built and queried in the test so its irrelevance is asserted rather than merely unexercised | — | S3, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The two tests. |
| 2 — something selects it | `Observe` is the only way a state enters the fleet, and `Report` the only way the condition leaves it. |
| 3 — the caller can discover it | `KindFleetWithdrawn` is declared with a named reader, as ADR-012 requires. |
| 4 — it is used | ⚠ **Nothing feeds the fleet on a served path.** There is no transport to carry replica states (`BACKLOG.md` §18), and no console to read the ledger (`BACKLOG.md` §25). This makes the condition expressible and keeps it out of the node's decision; a feeder arrives with a transport. |

## Mutation Log

- 2026-09-05 · 02e8933* · mutant killed · exit 1 · `internal/core/admit/fleet.go` · reports an empty fleet as all-withdrawn, so a group nobody has told us about raises an obligation as though it were saturated — a vacuous truth that manufactures an incident about a cluster that may be perfectly healthy, and does so loudest at startup when nothing has reported yet · acceptance-sha256:ea3a8a765ff42c5e09772ff84da2e1719127e94bc2ebf3ab94d160c0c36a17ef · covers:an all-withdrawn fleet being reported rather than resolved

## Invariants

- The fleet holds states, never controllers or ceilings.
- An obligation is raised only when EVERY replica has withdrawn.
- Recovery does not clear the obligation; only an acknowledgement does.
- Nothing on `Fleet` can change a node's own decision.

## Risks

- ⚠ **A fleet that holds `Controller`s could reach a node's decision.** It holds STATES — values a node reported — so there is nothing to reach through. A later convenience method taking a controller would undo rule 1 without touching `Decide`.
- ⚠ **THE SECOND MECHANISM WAS WITHDRAWN FROM `Rests-on:` RATHER THAN GIVEN A MUTANT THAT PROVED NOTHING.** "The fleet cannot change a node's decision" is a claim that a COUPLING DOES NOT EXIST, and mutation testing works by changing code that is there. Falsifying it requires ADDING the coupling — a package-level channel from `Fleet` to `Decide`, or a method taking a `*Controller` — which is a redesign, and a mutant that added one would be testing a program nobody wrote. ★ Recorded as `[proof: human]` on S3 and S5 with that reason, because a claim in `Rests-on:` that no mutant can bind is exactly what the gate exists to surface — and quietly leaving it there, unbound, is the failure. The structural guarantee is real: `Decide` takes no parameter, and `Fleet` holds no controller.
- ⚠ **`TestTheFleetCannotChangeANodesOwnDecision` must actually build and query the fleet.** A test that simply omits it proves the fleet is unused by omission, which is not a proof — omission is exactly what a later change would undo.
- ⚠ **Auto-resolving on recovery is the tempting behaviour** and it is ADR-038 rule 2 again: a cluster that shed everything and recovered is precisely what an operator should still see, and silence is not resolution.
- ⚠ **"All withdrawn" of an empty fleet must not read as all-withdrawn.** Zero replicas is an unknown fleet, not a saturated one, and a vacuous truth here would raise an obligation for a cluster nobody has told it about.

## Stop Condition

Stop and ask before adding a floor, an override, or anything that keeps a node
joined because its peers withdrew. That is ADR-039 rule 1's trap, and `BACKLOG.md`
§22 says choosing among the candidate responses needs a cluster to observe.

## Out of Scope

- Choosing what to DO when every replica has withdrawn (deferred: `docs/adr/BACKLOG.md` §22)
- Feeding the fleet replica states (deferred: `docs/adr/BACKLOG.md` §18 — there is no transport)
- Reading the obligation ledger (deferred: `docs/adr/BACKLOG.md` §25 — there is no console)

## Verification Log
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea3a8a765ff42c5e09772ff84da2e1719127e94bc2ebf3ab94d160c0c36a17ef · ms:3654
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea3a8a765ff42c5e09772ff84da2e1719127e94bc2ebf3ab94d160c0c36a17ef · ms:3594
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea3a8a765ff42c5e09772ff84da2e1719127e94bc2ebf3ab94d160c0c36a17ef · ms:3565
