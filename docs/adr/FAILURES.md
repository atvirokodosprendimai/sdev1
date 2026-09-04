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

Open entries, currently: **1**.

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
| `writer-process-lost` | ADR-017 | **unrecoverable and open** | The leaf becomes permanently read-only. Reads keep serving the published prefix correctly, but `TakeWriter` refuses forever and no append can ever succeed again. | **No operator action recovers this today; the leaf must be considered lost for writes.** See below for why it is not simply fixed. |

## The open entry, in full

### `writer-process-lost` — a leaf whose writer dies is read-only forever

**What was observed.** A tail hands out its writer token exactly once, because
two writers would compute the same slot for different entries. If the holder
disappears, nothing releases the token: `TakeWriter` returns false for the rest
of the process's life, and an append with any other token is refused with
`ErrWriterNotHeld`. Reads are unaffected and stay correct — the failure is
one-sided.

**Why it is not fixed here.** The obvious fix, a `ReleaseWriter` or a token that
any caller may claim after a timeout, is worse than the fault. It would let a
second writer take the leaf while the first is merely SLOW rather than dead —
paused by a garbage collection, a stalled disk, a network hiccup — and two live
writers appending to one tail is not a degraded system, it is a corrupted one.
The tail's whole correctness rests on there being exactly one.

Safe handover needs a fencing epoch: a monotonically increasing term, written
into the log, that makes a resurrected old writer's appends refusable. That
mechanism is `ADR-009`'s, and inventing a second one here would leave two
authorities over which node owns a leaf.

**So it is written down rather than patched.** The correct fix is a record that
does not exist yet, and a wrong fix would trade a leaf that stops for a leaf that
lies.

**When it closes.** ADR-009 lands, `TakeWriter` gains an epoch, and this entry is
re-run and re-dispositioned. ADR-019's Follow-ups carry the obligation.

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
