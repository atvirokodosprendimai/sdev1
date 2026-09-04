# ADR-019: Inject faults from a seed, and keep a written catalogue of every failure that does not recover

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/FAILURES.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/chaos/**`, `docs/adr/FAILURES.md`
**Enforced-by:** `internal/core/chaos/chaos_test.go::TestEveryInjectedFaultIsCatalogued`
**Invalidates:** none — checked; no record has yet said how this system is to be broken on purpose
**Served-path change:** None — this ADR changes only measurement and the record of what breaks. It adds no behaviour to any served path.

## Context

Every record in this corpus so far states how its mechanism fails and recovers.
None of them has been broken on purpose to find out whether that is true.

Two things force the question now. Six packages exist and pass, which means there
is finally something to break. And the durability record's central promise — that
a cluster refuses writes rather than accepting them at a durability nobody has —
is exactly the class of claim that is only ever tested by a fault.

**The test budget is 8GB of RAM, total.** That is a hard constraint and it shapes
the design rather than being an inconvenience to work around.

⚠ **A tight memory budget can MANUFACTURE the faults it then reports.** A
container killed by the kernel's out-of-memory killer looks, from the harness's
point of view, exactly like a node that crashed — which is the fault being
injected. A chaos suite that runs at the edge of its budget therefore produces
findings that are indistinguishable from artefacts of its own environment, and a
catalogue full of those is worse than no catalogue, because each entry costs a
day to disprove. Whatever runs must have headroom it can prove it had.

**And there is no node to run.** No transport, no storage engine, no binary that
serves anything: a composed cluster has nothing to compose. That is stated
plainly rather than worked around, and it splits this record in two.

## Existing Primitives Audit

- `internal/core/erasure` (ADR-006): already refuses below `k` and already
  excludes a fragment that fails its checksum. **Reused as a fault target**, not
  reimplemented — those refusals are catalogue entries with answers today.
- `internal/core/tail` (ADR-017): already makes an unpublished entry unreachable.
  **Reused as a fault target**: killing a writer mid-append is a fault this
  design claims to survive, and the claim is checkable in a single process.
- `internal/core/durability` (ADR-004): supplies the floor. **Reused** as the
  oracle for what a degraded cluster is supposed to do.
- `internal/core/segment` (ADR-005): supplies the checksum that turns corruption
  into a detected fault. **Reused.**
- A chaos library: **none adopted.** The faults here are "drop this fragment",
  "flip this bit", "stop this writer" over in-process values. A library that
  kills containers solves a problem this repository does not have yet, and the
  half it would solve is the half that is blocked.

## Decision

**Faults are injected from a seed, and every fault that does not recover is
written down whether or not it is fixed.**

1. **The catalogue is a first-class artifact**, `docs/adr/FAILURES.md`, and it is
   the deliverable. Every injected fault has an entry: what is injected, what is
   supposed to happen, what actually happened, and one of three dispositions —
   **recovers**, **unrecoverable by design**, or **unrecoverable and open**. The
   third carries either a fix or a written reason why there is none.

2. **"Unrecoverable by design" is a real and correct answer, not a failure.**
   Losing `m+1` fragments of a stripe destroys the block. There is no recovery,
   and a system that produced something anyway would be inventing. Cataloguing it
   as intended is what distinguishes it from the entries that are actually bugs —
   without that distinction a catalogue of twenty entries tells a reader nothing
   about which two matter.

3. **Every run is reproducible from a seed.** A fault schedule is a function of
   one integer, printed on failure and replayable. ★This is the decision that
   makes the whole exercise worth doing: a chaos run that fails once in a
   thousand and cannot be replayed produces a ticket nobody can close, and the
   effort goes into re-running rather than into fixing.

4. **In-process deterministic injection is the primary suite; a composed cluster
   is the secondary one.** Most fault CLASSES do not need separate machines —
   losing a fragment, corrupting a byte, stopping a writer, breaching the floor
   are all expressible over values. Simulation is reproducible, needs no
   orchestration and fits the budget with room to spare, so it is where the
   catalogue is filled in. The composed cluster covers only what genuinely needs
   real processes: partitions, clock skew between hosts, disk exhaustion, and the
   crash-restart path.

5. **The composed cluster declares its memory budget and proves its headroom.**
   Container limits are explicit, the sum is under the ceiling with margin, and a
   run that hits a container limit is reported as an ENVIRONMENT failure and never
   as a finding. A harness that cannot tell its own out-of-memory kill from an
   injected crash may not write to the catalogue at all.

6. **A fault that is injected but not catalogued fails the suite.** The gate is
   not "the system survived" — it is "every fault we injected has a written
   disposition". Otherwise a fault quietly stops being injected and the catalogue
   still reads as complete.

7. **The composed half is BLOCKED, and says so.** It waits on a node binary that
   serves reads and writes. Writing it against nothing would produce a harness
   that is green because it starts no cluster, which is the shape of gate this
   corpus exists to reject.

**What would falsify this.** If the in-process suite catalogues a fault as
recovering and the composed cluster later shows the same fault is fatal in a real
process, then simulation is not covering that class and the split in rule 4 is
wrong. That evidence cannot exist until the composed half runs, so it is recorded
as the thing to check rather than as a settled claim.

## Alternatives Considered

- **Only a Docker-composed chaos suite, no simulation.** Closest to production
  and what "chaos monkey" usually means. Rejected as the PRIMARY suite on three
  counts: it cannot run at all today; it is not reproducible, so a rare failure
  becomes a ticket nobody can close; and at this memory budget its own resource
  pressure is indistinguishable from the faults it injects. It is kept as the
  secondary suite because the classes simulation cannot reach are real.
- **Only simulation, no composed cluster ever.** Cheap, fast, reproducible.
  Rejected because a partition, a full disk and a process restart are not
  faithfully expressible in one process, and those are where distributed systems
  actually break.
- **A random fault schedule with no seed.** Simpler, and finds things. Rejected:
  an unreproducible failure is a report rather than a bug, and the cost lands on
  whoever tries to confirm it.
- **Catalogue only the failures that are fixed.** Tidier. Rejected outright — the
  unfixed ones are the entire value. A record of what this system does NOT
  survive is the thing an operator needs and the thing nobody writes down.
- **Adopt a chaos framework.** Rejected for now: the in-process faults are a few
  lines each over values this repository owns, and a framework aimed at killing
  containers addresses the half that is blocked.

## Component / Boundary Impact

One new component, `internal/core/chaos`, owning fault injection and the
catalogue's machine-checkable half. It has one reason to change: what a fault is.

⚠ The boundary: it injects and observes; it decides nothing. What SHOULD happen
under a fault is owned by the record that made the promise — ADR-004 for the
floor, ADR-006 for the coding tolerance, ADR-017 for the tail. A chaos package
that carried its own expectations would be a second statement of every guarantee,
and the two would drift.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `chaos.Fault` | new — one named, injectable fault with its expected disposition | T1 | T2, the catalogue check |
| `chaos.Schedule` | new — a seeded, replayable sequence of faults | T1 | T2 |
| `chaos.Disposition` | new — recovers, unrecoverable-by-design, unrecoverable-and-open | T1 | `docs/adr/FAILURES.md` |
| `docs/adr/FAILURES.md` | new — the catalogue itself | T1 | operators, and every later record |
| `deploy/chaos/compose.yaml` | new — the composed cluster and its declared memory limits | T2 (blocked) | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `chaos.Fault`, `chaos.Schedule`, `chaos.Disposition`, the catalogue format | T1 | T2 | No — T2 fills entries T1's format defines |

## Implementation

Two tasks. T1 runs now; T2 is blocked on a node binary and is marked so rather
than being written against nothing. See
`docs/adr/ADR-019-chaos-and-the-failure-catalogue/tasks/README.md`.

## Consequences

- **Positive:** The corpus's failure-and-recovery prose stops being assertion. A
  record that claims recovery has an entry showing it, or an entry saying it does
  not.
- **Positive:** What this system does NOT survive is written down in one place,
  which is what an operator actually needs and what is almost never recorded.
- **Positive:** A failure is replayable from an integer, so finding one and fixing
  one are the same activity rather than two separated by days.
- **Negative:** Simulation can be wrong in the same way the code is wrong. A fault
  model built from the same understanding that built the system will miss what
  that understanding missed, and the composed suite is the only correction for it.
- **Negative:** The catalogue is a document, so it can drift from the code. Rule 6
  makes the injected set machine-checked against it, which bounds the drift to the
  prose rather than the entries.
- **Neutral:** Half of this record ships later. That is visible in the task status
  rather than implied by silence.

## Out of Scope

- Anything requiring more than one process: partitions, host clock skew, disk exhaustion, crash-restart (deferred: this record's T2, blocked on a node binary)
- Performance under fault — how SLOW a degraded read is (deferred: `docs/adr/BACKLOG.md` §16)
- Automatic repair of anything a fault damages (deferred: `docs/adr/BACKLOG.md` §3)
- Deciding what SHOULD happen under a fault (permanent: boundary: each guarantee is owned by the record that made it; this package injects and observes, and a second statement of every guarantee would drift from the first)
- Faults in dependencies rather than in this system (permanent: boundary: a corrupted coding library or a lying filesystem is a threat model this record does not take on, and ADR-005's format version is the mechanism that would bound it)
- Adversarial faults — an attacker who can write to a disk (permanent: boundary: checksums here are detection codes for accidental corruption, the threat model ADR-005 and ADR-006 both chose; ADR-007 owns the key material a stronger model needs)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The out-of-memory killer produces findings that are artefacts of the 8GB budget | High, if unmanaged | High — every entry costs a day to disprove and the catalogue loses its credibility | Container limits declared and summed under the ceiling with margin; a run that hits a limit is reported as an environment failure and is forbidden from writing to the catalogue |
| A fault stops being injected and the catalogue still reads as complete | Med | High — a gate that has stopped looking is indistinguishable from a system that is healthy | `TestEveryInjectedFaultIsCatalogued` checks the injected set against the document, and fails on either an entry with no fault or a fault with no entry |
| Simulation misses a class the composed cluster would catch, and the catalogue reads as thorough | Med | Med | Rule 4 states which classes simulation does NOT cover, so the gap is written down rather than implied; T2 carries them |
| A chaos test is flaky and gets muted rather than fixed | Med | High — the muted one is usually the real bug | Every schedule is seeded and replayable, which removes the usual reason for muting |
| The catalogue becomes a list of things nobody intends to fix | Med | Low | The disposition column distinguishes intended from open, so the open ones stay countable rather than being lost among the intended ones |

## Rollback

No persistent state and no served path, so rollback is a code revert plus
deleting the catalogue.

⚠ Deleting the catalogue is the part with a cost that is not a code cost: it is
the only written record of what this system does not survive, and that knowledge
is not recoverable from the source. If this record is ever withdrawn, the
catalogue should outlive it.

## Follow-ups

- [ ] When a node binary exists, unblock T2 and re-check every entry simulation marked "recovers" — the split between the two suites is an assumption until something contradicts it.
- [ ] When ADR-009 lands, add the fault classes leader election introduces: a fenced writer that does not know it, and two nodes each believing they hold the leaf.
- [ ] When the segment writer lands (`BACKLOG.md` §12), add crash-during-seal, which `BACKLOG.md` §15 already flags as undecided rather than merely untested.
