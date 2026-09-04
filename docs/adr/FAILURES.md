# What this system survives, and what it does not

Every other document here describes what is supposed to happen. This one records
what happens when it does not, and it is the half an operator actually needs.

Governed by `ADR-019`. The checked catalogue below is machine-verified against
the faults registered in `internal/core/chaos`: every registered fault has an
entry here, and every entry names a registered fault. Both directions are
checked, because checking only the first catches a new fault nobody wrote up,
while checking the second catches a fault that quietly stopped being injected
while its entry still reads as current.

## Dispositions

There are exactly three, and deliberately no fourth — a fourth is how "we are
looking into it" enters a catalogue, after which nothing in it is countable.

- **recovers** — the system returned correct results, or refused correctly, and
  nothing was lost.
- **unrecoverable by design** — the data is gone and that is intended, because
  the information was not present. A correct answer, not a bug.
- **unrecoverable and open** — the system does not recover and it should. Every
  such entry carries a fix, or a written reason there is none.

Open entries, currently: **0**. One entry has been closed — see *Closed
findings* below, which is kept rather than deleted so the corpus records that
the failure existed and what fixed it.

⚠ Everything below was observed in a SINGLE PROCESS. The classes that need real
machines — network partition, host clock skew, disk exhaustion, crash and
restart — are not covered here, and are ADR-019's T2. An entry reading
"recovers" therefore means "recovers in simulation" until that suite runs.

## Checked catalogue

| Fault | Record | Disposition | What happens | What an operator does |
|-------|--------|-------------|--------------|-----------------------|
| `fragment-loss-within-tolerance` | ADR-006 | recovers | Two fragments of an `RS(4,2)` stripe are lost. The remaining four verify, the block is rebuilt byte-identical, and the read succeeds. | Nothing urgent. The stripe is now at zero remaining tolerance, so schedule a repair before another fragment goes. A degraded read costs `k` fragment fetches instead of one, so latency rises before anything fails. |
| `fragment-loss-beyond-tolerance` | ADR-006 | unrecoverable by design | Three fragments of an `RS(4,2)` stripe are lost, leaving three where four are needed. `Reconstruct` refuses with `ErrInsufficientFragments`, naming both counts. | The block is gone and no action recovers it — the information is not present, and a system that returned something anyway would be inventing. Restore from a backup taken under ADR-010, and treat the loss of `m+1` domains as the incident. The refusal is the correct behaviour and is not the fault to investigate. |
| `fragment-corruption` | ADR-006 | recovers | One fragment's bytes are altered. It fails its own checksum, is excluded as an *erasure* rather than trusted as data, and the block is rebuilt correctly from the rest. | Replace the fragment and investigate the medium that returned it. This is the case that would silently return WRONG DATA without per-fragment checksums, because a code with `m` parity corrects `m` known-missing fragments but only `⌊m/2⌋` that are present and lying. |
| `block-checksum-mismatch` | ADR-005 | recovers | A bit is flipped in a stored block. The checksum is verified before the codec runs, so `DecodeBlock` returns `ErrCorruptBlock` instead of handing rotten bytes to a decompressor. | Re-read from another replica or rebuild from the stripe. The error is raised *before* decompression deliberately: a decompressor fed rotten bytes fails confusingly at best and produces plausible garbage at worst. |
| `durability-floor-breached` | ADR-004 | recovers | A cluster degrades below the policy's `MinSize`. Writes are refused rather than accepted at a durability nobody has. Four copies inside ONE failure domain are also refused, because the floor counts distinct domains rather than copies. | Restore failure domains until the floor is met; writes resume by themselves. The refusal is the system working — a cluster that kept accepting writes here would be losing data quietly instead of loudly. |
| `writer-stopped-mid-append` | ADR-017 | recovers | The writer stops without releasing, flushing or cleaning up — what a killed process looks like from the tail's side. Every published entry is readable and whole; anything it had not published was never reachable. | Nothing is lost that was acknowledged. See the open entry below for what happens NEXT, which is the part that does not recover. |
| `writer-process-lost` | ADR-009 | recovers | The writer's process dies without releasing anything. A replacement is granted a strictly higher epoch without waiting for the one that vanished, and writes resume. Had the lost writer merely been PAUSED, its next append is refused at the tail with `ErrFencedOut` rather than corrupting anything. | Nothing, once a replacement holds the leaf. ⚠Was **unrecoverable and open** until ADR-009 — see *Closed findings*. |

## Closed findings

An entry is never deleted when it stops being true. Deleting it would leave no
record that the failure existed, which is most of what a catalogue is for.

### `writer-process-lost` — was: a leaf whose writer dies is read-only forever

**Closed by ADR-009 on 2026-09-04.**

**What was observed.** ADR-017's tail handed out its writer token exactly once,
because two writers would compute the same slot for different entries. If the
holder disappeared, nothing released the token: `TakeWriter` returned false for
the rest of the process's life, and an append with any other token was refused.
Reads were unaffected and stayed correct — the failure was one-sided and silent,
so a leaf looked healthy while accepting no writes ever again.

**Why it was not fixed immediately.** The obvious fix — a release call, or a
token any caller may claim after a timeout — is worse than the fault. It cannot
distinguish a DEAD holder from a SLOW one, so a garbage-collection pause, a
stalled disk or a network hiccup eventually lets a second writer take a leaf
whose first writer is still alive. Two live writers appending to one tail is not
a degraded system, it is a corrupted one. Trading a leaf that stops for a leaf
that lies is a bad trade, and the right response was to catalogue it and wait for
the mechanism that makes handover safe.

**What fixed it.** ADR-009 replaced the token with a lease carrying a
monotonically increasing epoch, and — the part that matters — put the check at
the RESOURCE. An append carries its epoch and the tail refuses anything below the
highest it has seen. A writer that asks "am I still the writer?" and then writes
has a window between the two in which it can lose the leaf, and no amount of
checking from the writer's side closes it. So a superseded writer is refused at
its next append, having been told nothing and having done no harm.

A grant never waits for the previous holder, which is what stops a dead writer
being a permanent outage; the epoch is what makes not waiting safe.

**What is still open behind it.** Consensus — who DECIDES a handover should
happen — needs a transport and is `BACKLOG.md` §19. The fencing is real; the
election is not, and the registry that grants epochs is in-process and named for
what it is.

## Known, but not yet injectable

These are not in the checked catalogue because nothing can inject them yet —
there is no code to break. They are recorded so they are not rediscovered as
surprises.

| Fault | Record | Why it cannot be tested yet |
|-------|--------|------------------------------|
| Crash during sealing | `BACKLOG.md` §15 | Sealing does not exist. It is also UNDECIDED rather than merely untested: a segment becoming readable and its tail entries becoming redundant are two publications, and a reader holding an older snapshot must still see a consistent view. |
| Network partition | ADR-009 | No transport, and no leader election to partition. |
| Host clock skew beyond the assumed bound | `BACKLOG.md` §4 | Skew is unpoliced by decision, not by oversight — nothing yet bounds or rejects it. |
| Disk exhaustion | `BACKLOG.md` §12 | Nothing opens a file. |
| Fragment index lost | ADR-006 | A fragment whose bytes survive but whose position is unknown is unusable, because the code solves for positions. What names a fragment on disk is part of the layout, which is undecided. |
| Two nodes each believing they hold a leaf | ADR-009 | The fault this corpus most needs to test, and the one furthest from being testable. |
