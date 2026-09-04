# ADR-007: Erase a subject by destroying its key, and never let the ciphertext name who it belonged to

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/crypt/**`
**Enforced-by:** `internal/core/crypt/crypt_test.go::TestShreddedSubjectIsUnreadable`
**Invalidates:** none — checked; ADR-005 reserved a per-block cipher identifier and left the cipher itself undecided, which is exactly the slot this record fills
**Served-path change:** An erasure request makes a subject's data unreadable everywhere at once — on disk, inside coded stripes, in offline replicas and in backups — without rewriting a single stored byte.

## Context

This system is append-only by construction. ADR-005 made a segment immutable,
ADR-006 spread it as fragments across failure domains, and ADR-010 will replay it
into backups. Every one of those decisions makes stored bytes harder to reach and
change, which is the point — and it makes deletion, in the ordinary sense,
impossible.

Erasure is nevertheless required. So the data cannot be destroyed and must
become unreadable, which leaves exactly one mechanism: encrypt per subject, and
destroy the key.

Destroying a key reaches places no delete can. A coded stripe scattered over ten
failure domains, a replica that has been offline for a month, a backup on a shelf
— none of them has to be found, visited or rewritten. They all become unreadable
at the same instant, because the thing that made them readable is gone.

⚠ **And the ciphertext is permanent.** That is the constraint everything else in
this record follows from. Whatever is stored beside those bytes is stored
forever, in a form nobody can erase — so if the block says whose data it is, the
subject's identity survives the erasure that was supposed to remove it. An
identifier is personal data. A system that shreds the content and keeps an
un-erasable label naming the person has not erased anything; it has produced a
permanent record of the fact that this person's data was deleted, which is very
nearly the opposite of what was asked.

Two open follow-ups named this record and are closed here. ADR-005 asked that the
cipher identifier be per block and that the key identifier not sit beside the
ciphertext in a way that defeats destroying the key. ADR-006 asked that coding
run after encryption, so a fragment checksum covers ciphertext rather than
plaintext structure.

## Existing Primitives Audit

- `internal/core/segment` (ADR-005): supplies `CipherID`, `CipherNone`,
  `StageEncrypted`, and the fixed pipeline position. **Reused whole and not
  amended.** The block header already has everything this record needs from it,
  which is why the envelope below prefixes the ciphertext instead of widening a
  format that is already written.
- `internal/core/erasure` (ADR-006): codes whatever bytes it is given.
  **Unchanged.** Encryption running before coding means fragments carry
  ciphertext, and their checksums check the bytes that are actually on a disk.
- `internal/core/addr` (ADR-016): a tenant is a contiguous subtree. **Relied on**
  for scope: a per-tenant key namespace falls out of the addressing model and
  does not need inventing here.
- `crypto/aes` and `crypto/cipher`: **used directly.** AES-256-GCM is in the
  standard library, is hardware-accelerated, and is authenticated — a wrong or
  destroyed key produces an ERROR rather than plausible garbage, which matters
  more here than in most places because "unreadable" is the guarantee.

## Decision

**A subject's datoms are encrypted under a key that belongs to that subject
alone, and erasure is the destruction of that key.**

1. **One key per subject.** Per tenant is too coarse — one person cannot be
   erased without erasing everyone. Per block is too fine: a subject's blocks
   would die one at a time, and erasure would become a sweep rather than an act.
   ADR-003 already made the entity the transaction boundary, so the entity is the
   natural unit here too.

2. **The key lives in a mutable keystore; the ciphertext lives in immutable
   storage. They are never the same place.** This is what makes destruction
   possible at all: an immutable segment cannot give up a key it contains.

3. **A block carries an opaque KEY HANDLE, never the subject's identity.** The
   handle is random, allocated once per subject, and not derived from the
   subject in any way. The mapping from subject to handle lives in the keystore
   and is destroyed with the key. ★This is the rule the Context argues for: after
   shredding, the ciphertext remains forever, so anything readable beside it
   remains forever. A handle that could be computed from an email address would
   make every shredded block a permanent record of that address.

4. **The envelope prefixes the ciphertext and changes no existing format.**
   Stored bytes are `handle ‖ nonce ‖ ciphertext‖tag`. The block header's
   `CipherID` says which envelope layout applies, which is precisely what ADR-005
   put that field there for. No block header widens, no format version changes,
   and nothing already written is reinterpreted.

5. **Decryption takes the keystore, not a key.** The envelope says which key; the
   keystore says whether you may still have it. A caller cannot supply a key it
   found somewhere, and a shredded subject fails at the same place for everyone.
   ★Same shape as ADR-005's `DecodeBlock`, which takes no configuration: what is
   needed to read the bytes comes from the bytes, and permission comes from the
   one authority that can revoke it.

6. **Destruction is irreversible, and the audit record names no subject.** A
   shred writes `{handle, when, request reference}` and nothing else. Proving
   "this person was erased" is done through the requester's own reference, held
   outside this system — because a durable record binding a handle to a name
   would reintroduce exactly what rule 3 removes.

7. **A random nonce per block, carried in the envelope.** Blocks are written once
   and never rewritten, and a per-subject key sees far fewer blocks than the
   birthday bound for a 96-bit random nonce. A deterministic nonce derived from
   position would be smaller and is rejected: it survives only while nothing ever
   re-encodes a block at the same coordinates, which is an assumption about
   future code rather than a property of this one.

8. **Encryption runs after compression and before coding**, which ADR-005 already
   fixed and this record does not revisit. Ciphertext does not compress, so
   compressing afterwards is worthless; and coding afterwards means fragment
   checksums cover the bytes that sit on disks rather than plaintext structure.

**What would falsify this.** A plaintext copy of a shredded subject's data
surviving anywhere the key destruction does not reach. The known candidate is an
index that stores attribute VALUES rather than references — such an index is a
plaintext copy by another name, and no key destruction touches it. That is stated
as a constraint on work not yet done (`BACKLOG.md` §15) rather than as a solved
problem, because the index does not exist and the constraint must exist before it
does.

## Alternatives Considered

- **Rewrite the log to remove the subject.** The obvious reading of "delete".
  Rejected: every record in this corpus is built on immutability, and rewriting
  would have to find and revisit every coded stripe, every offline replica and
  every backup — visiting what you cannot enumerate, and failing silently for
  whatever you miss.
- **A tombstone that hides the subject from queries.** Cheap, reversible, and
  what "delete" often means in practice. Rejected AS ERASURE, though ADR-010
  keeps it as a separate mechanism under a different name: marking makes data
  invisible, and invisible is not erased. Anyone with the bytes still has them.
- **One key per tenant.** Simpler key management, fewer keys. Rejected: erasing
  one person would erase the tenant. It remains useful as a NAMESPACE — ADR-016's
  subtree gives that for free — but not as the unit of destruction.
- **One key per block.** Fine-grained, and keys never outlive their data.
  Rejected: erasing a subject becomes a sweep over every block it ever touched,
  which is the enumeration problem this record exists to avoid, one level down.
- **Store the key handle in the block header rather than the envelope.** Tidier,
  and headers are already fixed-width. Rejected: it widens a format that is
  already written, forcing a version change and a migration, to gain nothing —
  the envelope is read at the same moment the header is.
- **A deterministic nonce from (leaf, block index).** Smaller, and needs no
  storage. Rejected under rule 7: nonce reuse under one key is catastrophic for
  GCM, and determinism makes safety depend on nothing ever re-encoding a block at
  the same coordinates — an assumption about code not yet written.
- **A handle derived from the subject, such as a hash of the entity.** No
  allocation, no mapping to keep. Rejected outright: a hash of an email address
  is a permanent, un-erasable identifier for that address sitting beside the
  ciphertext forever, and it is confirmable by anyone who guesses the address.

## Component / Boundary Impact

One new component, `internal/core/crypt`, owning the envelope, the cipher and
the keystore contract. It has one reason to change: how a subject's bytes are
made unreadable.

⚠ The boundary that matters: this component decides how erasure WORKS. It does
not decide WHEN, WHO may ask, or what else an erasure request triggers — ADR-010
owns the request's fan-out to sinks, and ADR-016's open authorization question
(`BACKLOG.md` §11) owns who may make one. A crypt package that also authorized
would be a second gate over the same door.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `crypt.KeyID` | new — an opaque 16-byte handle, random and not derived from the subject | T1 | T2, storage |
| `crypt.Key` | new — a 256-bit key, never persisted beside ciphertext | T1 | T2 |
| `crypt.CipherAES256GCM` | new — the `segment.CipherID` this envelope claims | T1 | ADR-005's readers |
| `crypt.Seal` / `crypt.Open` | new — `Open` takes the keystore, never a key | T1 | storage |
| `crypt.Keystore` | new — allocate, fetch, resolve, destroy | T2 | T1's `Open`, and callers |
| `crypt.ErrKeyDestroyed` | new sentinel — the subject was erased | T2 | callers |
| `crypt.ErrNotEncrypted` | new sentinel — the bytes carry no envelope | T1 | callers |
| `crypt.ShredRecord` | new — `{handle, when, request}`, and deliberately no subject | T2 | audit |
| `segment.CipherID` | consumed unchanged — no field is added to any header | — | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `crypt.KeyID`, `crypt.Key`, the envelope, `crypt.Seal` | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `crypt.Keystore`, `crypt.ErrKeyDestroyed`, `crypt.ShredRecord` | T2 | T1's `Open` resolves through it | No — `Open` takes the interface, so T1 declares it and T2 implements it |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-007-crypto-shredding/tasks/README.md`.

## Consequences

- **Positive:** Erasure reaches everything at once — coded stripes, offline
  replicas, backups — without finding, visiting or rewriting any of it.
- **Positive:** Erasure is O(1) and immediate. Destroying one key is not a sweep,
  so it cannot be half-done, and it does not compete with repair traffic.
- **Positive:** A shredded subject fails closed. GCM is authenticated, so a
  missing key produces an error rather than plausible garbage.
- **Negative:** Erasure is irreversible and instant, which makes an erroneous
  shred unrecoverable. There is no undo by construction, and that is the same
  property that makes it work.
- **Negative:** ⚠ **A backup containing both the keystore and the data defeats
  the whole mechanism.** Restoring it resurrects the key alongside the ciphertext.
  The keystore's backup is a separate concern with a separate retention, and this
  is the single easiest way to get crypto-shredding wrong.
- **Negative:** Every read of an encrypted subject costs a key fetch and a
  decryption. The key fetch is the expensive half, and caching it is a decision
  nobody has made yet.
- **Neutral:** Compression happens before encryption, so ciphertext size still
  leaks the compressibility of the plaintext. That is inherent to the fixed
  pipeline order ADR-005 chose for good reasons, and it is recorded rather than
  hidden.

## Out of Scope

- Who may request an erasure, and how that request is authorized (deferred: `docs/adr/BACKLOG.md` §11)
- What else an erasure request triggers across sinks and backups (permanent: boundary: ADR-010 owns the fan-out and its per-sink acknowledgement; this record owns only what makes the bytes unreadable)
- Where the keystore is actually persisted, and how it is itself made durable (deferred: `docs/adr/BACKLOG.md` §17)
- Key rotation, and re-encrypting a subject under a new key (deferred: `docs/adr/BACKLOG.md` §17)
- Caching decrypted plaintext or fetched keys (deferred: `docs/adr/BACKLOG.md` §17)
- Preventing a plaintext copy from being created elsewhere, such as by an index over attribute values (deferred: `docs/adr/BACKLOG.md` §15)
- Defending against an adversary who obtained a key before it was destroyed (permanent: boundary: crypto-shredding makes data unreadable going forward; it cannot un-disclose what already leaked, and no mechanism can)
- Choosing a cipher other than AES-256-GCM (permanent: fact: AES-GCM is in the Go standard library and hardware-accelerated on the target platforms, so it needs no dependency and breaks no pure-Go build; citation: url https://pkg.go.dev/crypto/cipher)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A backup captures the keystore alongside the data, so a restore resurrects a destroyed key | High, if unmanaged | Critical — erasure is silently undone and nobody finds out | Stated as a consequence and carried into ADR-010's follow-ups; the keystore is a separate backup with separate retention |
| The key handle turns out to be derivable from the subject, leaving a permanent identifier beside permanent ciphertext | Low after this record, near-certain without it | Critical — the erasure retains the identity it was asked to remove | Handles are random and allocated; a test asserts two allocations for one subject differ and that no subject bytes appear in a handle |
| A nonce repeats under one key | Low | Critical — GCM loses confidentiality and integrity on nonce reuse | 96-bit random nonce per block, far below the birthday bound for a per-subject key; a test asserts nonces differ across many seals |
| An index or projection stores plaintext values, so a shredded subject is still readable there | Med | High — the guarantee is void and nothing reports it | Recorded as a constraint on `BACKLOG.md` §15 BEFORE the index is designed, which is the only point at which it is cheap |
| An erroneous shred destroys data nobody meant to erase | Med | High — unrecoverable by construction | Out of scope here by design: the request path and its authorization are ADR-010's and `BACKLOG.md` §11's, and that is where a confirmation belongs |

## Rollback

The envelope is persistent state, so rollback is not a code revert.

Before any encrypted data is written, reverting is deleting the package. After,
it is not reversible: blocks carry envelopes, and reading them requires this code
and the keystore. Disabling encryption for NEW writes is available at any time —
the block header records `CipherNone` and old blocks keep their envelopes —
because the cipher is recorded per block rather than held in configuration, which
is the same property ADR-005 and ADR-006 both rely on.

⚠ A shred has no rollback at all, and that is deliberate. A destruction that
could be undone would not be an erasure.

## Follow-ups

- [ ] When ADR-010 lands, confirm the keystore is excluded from the data backup and has its own retention — a backup holding both defeats this record entirely, and it is the easiest mistake to make.
- [ ] When an index is designed (`BACKLOG.md` §15), confirm it stores references rather than attribute values for encrypted subjects; an index over plaintext values is a plaintext copy that no key destruction reaches.
- [ ] When the segment writer lands (`BACKLOG.md` §12), confirm `StageEncrypted` is set on the block header by the sealer and that a block claiming it always carries a parseable envelope.
- [ ] Decide keystore persistence, rotation and caching before the first real deployment (`BACKLOG.md` §17); an in-memory keystore erases everything on restart, which is safe in the wrong direction.
