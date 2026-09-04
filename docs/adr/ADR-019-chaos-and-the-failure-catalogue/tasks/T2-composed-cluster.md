# Task ADR-019-T2: The composed cluster, and the fault classes one process cannot reach

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `deploy/chaos/compose.yaml`, the composed-cluster entries of `docs/adr/FAILURES.md`
**Consumes:** `chaos.Fault`, `chaos.Disposition`, `chaos.Schedule`, the catalogue format (T1)
**Data dependency:** needs a running composed cluster of node processes with declared memory limits, and a host with at least 8GB available to the container runtime
**Proof map:** v1
**Rests-on:** `the harness distinguishing an out-of-memory kill from an injected crash`, `the declared container limits summing under the ceiling`

## Goal

Cover the fault classes that need real processes — partition, host clock skew,
disk exhaustion, crash and restart — without letting the test host's own limits
manufacture findings.

⚠ **This task cannot pass yet, and it is `pending` rather than `blocked`.** There
is no node binary — no transport, no storage engine, nothing that serves — so
there is no cluster to compose. It is NOT `blocked`, because nothing outside this
repository is being waited on: the node binary is work this project will do, and
calling that "blocked" would move an unfinished task into a status that implies
nobody here can make it sooner.

The fence stays runnable and stays red. A compose file written against nothing
would start no cluster, inject nothing, and pass — which is exactly the shape of
gate this corpus exists to reject, and why the fence fails on a skip.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `deploy/chaos/compose.yaml` | add | The cluster and, more importantly, its DECLARED memory limits. |
| `internal/core/chaos/compose.go` | add | Driving the cluster, injecting the multi-process faults, and classifying a run as finding or environment failure. |
| `internal/core/chaos/compose_test.go` | add | The tests below, skipped with a stated reason when no cluster is reachable. |
| `docs/adr/FAILURES.md` | edit | The entries only this task can fill. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestComposedClusterFitsTheMemoryBudget`, `TestOutOfMemoryKillIsNotAFinding`, `TestPartitionedClusterRefusesBelowTheFloor`, `TestCrashDuringWriteLosesNothingAcknowledged`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Size the cluster from the budget rather than the other way round. ADR-006's `RS(k,m)` needs `k+m` failure domains, so `RS(4,2)` is six nodes and `RS(8,2)` is ten. Write down which scheme the 8GB ceiling actually permits, with the arithmetic. [proof: human: a reader checks the declared limits SUM to less than the ceiling with margin, and that the margin is stated as a number rather than as a word]
3. [S3] Declare an explicit memory limit per container and assert in the test that their sum plus the harness is under the ceiling. ★A limit that is not declared is not a limit; the kernel's is, and it arrives as a kill.
4. [S4] Classify every terminated container: an out-of-memory kill is an ENVIRONMENT FAILURE, never a finding, and a run containing one may not write to the catalogue. ★A container the kernel killed looks exactly like a node that crashed — which is the fault being injected — so a suite that cannot tell them apart manufactures the findings it reports.
5. [S5] Inject the multi-process faults: partition, host clock skew beyond ADR-002's assumption, disk exhaustion, crash and restart.
6. [S6] Assert against the record that made each promise — ADR-004's floor for the partition case, ADR-006's tolerance for fragment loss across real hosts — rather than restating the guarantees here.
7. [S7] Fill the catalogue entries this task covers, and re-check every entry T1 marked "recovers" now that a real process is involved. ★The split between simulation and a real cluster is an assumption until something contradicts it.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/chaos/... -run 'TestComposedCluster|TestOutOfMemoryKill|TestPartitionedCluster|TestCrashDuringWrite' -count=1 2>&1 | tee /tmp/adr019-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|SKIP.*cluster" /tmp/adr019-t2.out
```

⚠ `SKIP.*cluster` is in the grep deliberately. These tests skip when no cluster is
reachable, which is right for a developer's laptop and WRONG as evidence: a
skipped run exits 0 and would otherwise record this task as verified while
nothing was exercised. The fence therefore fails on a skip, and this task cannot
reach `done` from a machine that cannot start the cluster.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestComposedClusterFitsTheMemoryBudget` | `internal/core/chaos/compose_test.go` | Every container declares a memory limit and their sum plus the harness is under the ceiling with a stated margin | — | S2, S3 |
| `TestOutOfMemoryKillIsNotAFinding` | `internal/core/chaos/compose_test.go` | A container terminated by the out-of-memory killer is classified as an environment failure, and a run containing one writes nothing to the catalogue | — | S4 |
| `TestPartitionedClusterRefusesBelowTheFloor` | `internal/core/chaos/compose_test.go` | A partition that puts a leaf below ADR-004's `MinSize` causes writes to be REFUSED rather than accepted at a durability nobody has | — | S5, S6 |
| `TestCrashDuringWriteLosesNothingAcknowledged` | `internal/core/chaos/compose_test.go` | A node killed mid-write loses nothing it acknowledged, and anything unacknowledged is absent rather than partially present | — | S5, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | The compose file is read by the harness, and `TestComposedClusterFitsTheMemoryBudget` fails if a container is added without a declared limit. |
| 3 — the caller can discover it | `deploy/chaos/compose.yaml` is the interface an operator reads to reproduce a run. |
| 4 — it is used | The catalogue entries this task fills are the measurement. |

## Mutation Log

## Invariants

- Every container declares a memory limit, and the sum is under the ceiling with a stated margin.
- An out-of-memory kill is an environment failure and never a finding.
- A run containing an environment failure writes nothing to the catalogue.
- Assertions are made against the record that made the promise, never restated here.

## Risks

- ⚠ **The budget can manufacture the findings.** This is the risk that makes the task hard rather than long: a kernel kill and an injected crash are the same event from outside, and a catalogue polluted with the first loses the credibility that makes the second worth reading. S4 is the whole mitigation and it is asserted rather than assumed.
- A skipped test exits 0. The fence greps for the skip, so a machine that cannot start the cluster cannot record this task as verified.
- `Data dependency` is NOT hermetic here, and that is the point: the sign-off must record what the run was taken against — the scheme, the node count, the declared limits and the observed peak.

## Stop Condition

Stop if the ceiling does not permit a cluster with `k+m` distinct failure
domains. Running the scheme with fewer domains than it requires tests something
other than the system: ADR-004 refuses such a policy at load, so the harness
would be measuring a configuration the product does not allow.

## Out of Scope

- Faults inside dependencies rather than inside this system (permanent: boundary: ADR-019 states this threat model; a lying filesystem or a corrupted coding library is not taken on)
- Performance under fault (deferred: `docs/adr/BACKLOG.md` §16)
- Automatic repair (deferred: `docs/adr/BACKLOG.md` §3)

## Verification Log
