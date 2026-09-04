# ADR-014: A filesystem path is a query, the tree is read-only, and history is a path prefix

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-013-agent-tool-surface.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/vfs/**`
**Enforced-by:** `internal/core/vfs/vfs_test.go::TestAPathCompilesToAQuery`
**Invalidates:** none — checked; ADR-011 fixed the language and left every surface over it open, and ADR-007 fixed what erasure destroys without saying what a caller sees afterwards
**Served-path change:** An operator mounts the store and reads an entity's attributes as files, and reads them as they stood at any instant by prefixing the path — where before the only way in was the query language itself.

## Context

The store is bitemporal and append-only, which means it already holds every
version of every fact. A filesystem projection over it is therefore a natively
snapshotting filesystem, and it costs almost nothing to expose: an entity is a
directory, an attribute is a file, and an instant is a path prefix. Every tool
that walks a directory tree — `cp`, `rsync`, `diff -r`, `grep -r`, `make`, a
backup agent — gets time travel with no knowledge of this system at all.

★ **That is the entire reason to build it.** Not "a filesystem is a friendly API"
— a filesystem is a *worse* API than the query language for almost everything.
It is worth building because it is the one interface tens of thousands of existing
programs already speak, and because history is free here in a way it is nowhere
else.

Which sets up the two ways to get it wrong.

⚠ **Writes.** A filesystem that accepts writes looks obviously better and is the
thing this must not be. ADR-003 made the entity the transaction boundary; POSIX
gives a program no way to say "these three attributes change together". A
writable projection would commit each `write(2)` as its own transaction and
silently break the only atomicity guarantee the store makes — silently, because
the caller's writes all succeed.

⚠ **Metadata that lies.** A filesystem's callers do not read the values; they read
`stat`. `make` compares mtimes. `rsync` compares mtime and size. A backup agent
skips a file whose mtime has not moved. If mtime were the clock at read time,
every file would look modified on every pass, and every incremental tool over
this mount would copy everything, every time — a projection that is correct in
its contents and useless in practice.

## Existing Primitives Audit

- `internal/core/ql` (ADR-011): supplies `Statement`, `Select` and `TimeClause`.
  **Reused whole and made the only target** — a path compiles to a statement, for
  ADR-013's reason: a second path to an answer is a second set of semantics.
- `internal/core/temporal` (ADR-002): reached only through `TimeClause`, never
  directly, so the defaults table stays single-implementation.
- `internal/core/mcpsurface` (ADR-013): **not reused, and deliberately not shared.**
  The two surfaces answer to different callers with different constraints — an
  agent's tenant must not be nameable, a mount's is fixed at mount time — and a
  shared "surface" abstraction over two members would be an abstraction invented
  for its second use.
- A FUSE library: **none yet.** The mapping from a path to a query is a pure
  function and needs no kernel; the mount is T2.
- `path/filepath`: **not used.** ⚠ Its `Clean` resolves `..`, which is precisely
  the behaviour this record refuses — see rule 6.

## Decision

**A path is a query. The tree is read-only, refused at open. An instant is a path
prefix, and metadata tells the truth about time.**

1. **A path that names data compiles to a `ql.Statement`.** `/e/<entity>` is every
   attribute of an entity; `/e/<entity>/<attribute>` is one. ★Same rule as
   ADR-013 and the same reason: a handler that reaches storage directly is a
   second query surface with its own time semantics.

2. **An instant is the path prefix `/.at/<instant>`, and a snapshot path is an
   ORDINARY path.** ★This is what makes `cp -r /.at/<instant>/e /backup` work with
   no filesystem-specific tooling — which is the whole point of the projection.

3. **The projection is READ-ONLY, and a write is refused at `open`.** ⚠ Not at
   `write` and never at `close`. A program that opens for writing, buffers, and
   fails at `close(2)` has already lost the data, and a great many programs do not
   check `close` at all. The refusal is `EROFS` and it is decided from the
   caller's INTENT before anything else about the path is considered, so
   `open("/e/x", O_WRONLY)` is `EROFS` rather than `EISDIR` — reporting `EISDIR`
   would tell the caller that opening a *file* for writing would have worked.

4. **`mtime` is the datom's transaction time; `atime` is the read.** ⚠ Two reads
   of an unchanged fact report the same mtime. Anything else makes every
   incremental tool over this mount copy everything on every pass.

5. **There are exactly three node kinds: the root, an entity directory, and an
   attribute file.** ⚠ No control files. A file that changes behaviour when
   written is a write surface reintroduced behind rule 3's refusal, and rule 3
   would still report `EROFS` while the behaviour changed.

6. **`.` and `..` are REFUSED, not resolved.** ⚠ Inside a snapshot prefix a
   resolved `..` climbs out of the snapshot, and the caller who asked for history
   gets a confident answer from the wrong time. Refusing with `EINVAL` is a
   caller-visible failure; resolving is a silent one.

7. **A shredded datom is `ENOENT`, exactly like one that never existed.** ⚠ Not
   `EACCES` and not an empty file. `EACCES` confirms the entity exists, which
   ADR-007 spent a whole record making impossible — a permission error is an
   oracle, answerable by anyone who can guess a name. An empty file would make
   erasure look like a blank value, which is ADR-011's unbound-versus-empty
   conflation with the consequences of a deletion request behind it.

**What would falsify this.** A path that resolves to something other than a query.
That is the falsifier in `Enforced-by:`, it needs no kernel and no storage engine,
and it is the mistake a reasonable implementation makes — reading a datom directly
is shorter than building a statement and handing it somewhere.

## Alternatives Considered

- **Make it writable, mapping `write(2)` onto a transaction.** The obviously more
  useful filesystem, and what every reviewer will ask for. Rejected under rule 3:
  POSIX has no way to express that several attributes change together, so the
  entity transaction boundary ADR-003 chose would be broken per `write(2)` — and
  broken silently, since each write succeeds.
- **Accept writes and refuse at `close`.** Lets a program run further before
  failing. Rejected under rule 3: by `close` the data is already gone, and
  unchecked `close` is common enough that the failure would frequently be silent.
- **Expose history as a `.history/` directory inside each entity.** Reads more
  naturally, keeps everything about an entity in one place. Rejected under rule 2:
  it makes time a per-node feature, so a tool walking `/e` descends into every
  entity's entire history by accident, and a snapshot stops being a tree anything
  can copy.
- **Return `EACCES` for a shredded subject, so the caller knows why.** Kinder
  error reporting. Rejected under rule 7: distinguishing "erased" from "never
  existed" is an oracle for the subject's existence, and ADR-007's erasure is
  worth nothing if a `stat` confirms who was erased.
- **Return an empty file for an absent attribute.** Avoids `ENOENT` handling in
  scripts. Rejected under rule 7: it conflates "no such fact" with "the fact is
  the empty string", which is the exact conflation ADR-011 has a rule against.
- **Resolve `..` the way a real filesystem does.** Least surprising. Rejected
  under rule 6: inside a snapshot it silently returns live data for a historical
  question.
- **Offer a control file to set the snapshot instant for the session.** Avoids
  long paths and is a common trick. Rejected under rule 5: it is a write surface
  behind the read-only refusal, and it makes a path's meaning depend on hidden
  state, so two processes reading the same path get different answers.
- **Share one "surface" abstraction with ADR-013.** Less code. Rejected: the two
  have opposite constraints on the tenant — an agent must not be able to name one,
  a mount fixes one at mount time — and an abstraction over two members with
  different invariants generalises the wrong thing.

## Component / Boundary Impact

One new component, `internal/core/vfs`, owning what a path means. It has one
reason to change: the mapping between a filesystem tree and this store's shape.

⚠ The boundary: it PARSES and COMPILES, and decides dispositions. It mounts
nothing, reads nothing, and holds no state. `ParsePath` is a pure function, and so
are `Open`, `Stat` and `StatAttr` — they take what the store said and return what
the kernel should be told. Keeping the mapping separate from the mount is what
makes every rule above testable with no kernel, no FUSE library and no evaluator.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `vfs.Kind` | new — the three node kinds and no fourth | T1 | T2 |
| `vfs.Path` | new — a parsed path: entity, attribute, instant | T1 | T2 |
| `vfs.Errno` | new — the dispositions this projection returns | T1 | T2 |
| `vfs.ParsePath` | new — the grammar, including what it refuses | T1 | T2 |
| `vfs.Path.Compile` | new — the statement a path means | T1 | T2 |
| `vfs.OpenFlags` / `vfs.Open` | new — write refused at open | T1 | T2 |
| `vfs.Presence` / `vfs.Stat` | new — absent and shredded are one answer | T1 | T2 |
| `vfs.Datom` / `vfs.Attr` / `vfs.StatAttr` | new — metadata that tells the truth about time | T1 | T2 |
| FUSE mount | new, `pending` — `cmd/sdev1-mount` | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `vfs.Path`, `vfs.ParsePath`, `vfs.Open`, `vfs.Stat`, `vfs.StatAttr` | T1 | T2 | No — T2 is written against T1 |

## Consequences

- **Positive:** Every program that walks a directory tree reads this store, and
  reads it at any instant, without knowing it exists.
- **Positive:** History costs nothing to expose, because the storage model already
  holds it — the projection reveals a property rather than building one.
- **Positive:** The whole grammar is a pure function, so the rules are provable
  before a kernel is involved.
- **Negative:** Read-only is a real limitation, and the request to lift it will
  recur. The answer is a transaction through the query language, and it will keep
  being unsatisfying to whoever wanted `echo > file`.
- **Negative:** Listing entities has no query behind it — the language expresses
  reads of a named entity, not enumeration — so the root directory is not fully
  implementable yet.
- **Negative:** Refusing `..` makes the mount behave unlike a POSIX filesystem in
  one visible way, and some tools will trip on it. That is preferred to a silent
  escape from a snapshot.
- **Neutral:** Nothing mounts. The grammar is decidable and the mount is not.

## Out of Scope

- Mounting anything, which needs a FUSE library (deferred: `docs/adr/BACKLOG.md` §26)
- Evaluating a compiled statement against storage (deferred: `docs/adr/BACKLOG.md` §20)
- Listing entities, which the language cannot yet express (deferred: `docs/adr/BACKLOG.md` §26)
- Writes through the mount, which need a transaction the path cannot express (deferred: `docs/adr/BACKLOG.md` §26)
- Which tenant a mount is bound to, and who may mount it (deferred: `docs/adr/BACKLOG.md` §11)
- Caching an attribute's bytes for a re-read (deferred: `docs/adr/BACKLOG.md` §24)
- What POSIX requires of a filesystem (permanent: fact: the semantics of open, stat, mtime and the errno values used here are defined by the platform rather than by this record; citation: url https://pubs.opengroup.org/onlinepubs/9699919799)
- Whether a filesystem is a good interface for this data (permanent: boundary: this record decides what the mapping MEANS if one exists; it is built because tens of thousands of existing programs already speak it, not because it is the best expression of the model)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Writes are added because a read-only filesystem feels crippled | High — it is the first thing anyone asks for | Critical — each `write(2)` becomes its own transaction and ADR-003's entity boundary is broken silently | Rule 3, `EROFS` decided from intent at open, and a test that opens every node kind for writing |
| A path is answered by reading a datom directly, because building a statement is longer | High | High — a second query surface with its own time semantics, diverging on historical reads | The falsifier compiles every data-naming path and asserts a statement comes back |
| `mtime` is set from the clock, which is what a naive `stat` does | High — it is the default in most filesystem examples | High — every incremental tool copies everything on every pass, and the mount looks broken rather than wrong | Rule 4, and a test that reads the same unchanged datom twice and compares |
| `..` is resolved for POSIX compatibility | Med | High — a snapshot read silently returns live data | Rule 6 and a test that walks out of a snapshot prefix |
| A shredded subject returns `EACCES` because it is more informative | Med | Critical — a `stat` becomes an oracle for who was erased, undoing ADR-007 | Rule 7, and a test asserting absent and shredded are byte-identical answers |
| A control file is added to set the snapshot instant | Med | High — a write surface behind the read-only refusal, and a path whose meaning depends on hidden state | Rule 5 and a test that no path parses to a fourth node kind |

## Rollback

No persistent state and no format. Parsing is a pure function and nothing is
mounted, so a revert is a code revert. An operator loses a mount; nothing stored
changes.

## Follow-ups

- [ ] When the mount lands (`BACKLOG.md` §26), confirm the kernel sees `EROFS` from `open` and not from `write` — a FUSE library that defaults to reporting write failures at write time would undo rule 3 without changing a line of this package.
- [ ] When the evaluator exists (`BACKLOG.md` §20), confirm a shredded subject reaches `Stat` as `PresenceShredded` rather than as an error; rule 7 is only as good as what the layer below reports.
- [ ] Measure an incremental tool — `rsync -n` over two passes with no writes between them — and confirm it copies nothing. Rule 4 is reasoned, and the failure it prevents is invisible except at scale.
- [ ] When the language can enumerate (`BACKLOG.md` §20), decide what the root directory lists; today it names no query, and a mount that answers an unanswerable question by inventing entries would be worse than one that returns nothing.
