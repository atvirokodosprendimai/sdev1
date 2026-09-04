# sdev1

A **planetary-scale structural data engine**, written in Go.

Facts are stored as EAVT datoms on two independent time axes, written through a
CQRS command path onto an append-only log, and served from read models with their
own shapes. The key space is a trie of 256-way fan-out. Storage is a binary
segment format; sealed segments are erasure-coded; live segments are replicated
whole under per-shard leader election.

Nothing is ever overwritten. A correction is a new fact, so "what was true then"
and "what did we believe then" stay separately answerable — forever.

---

## What you can actually do today

**Short version: you can parse any query, and run search and addressing in
memory. You cannot store a fact or read one back, because there is no storage
engine.**

| | Status | What that means |
|---|---|---|
| `SELECT … FROM … WHERE … AS OF … TRANSACTION …` | **parses** | The tree is correct and the time defaults resolve. Nothing executes it. |
| `MATCH SHAPE LIKE … REQUIRE … OPTIONAL … SIMILARITY …` | **parses** | Same. Per-leg time qualifiers work. |
| `SEARCH … IN … FACET BY … LIMIT …` | **parses + runs** | Against an **in-memory** index: real ranking, real facets, erasure honoured. Not persisted. |
| Backtick-quoted identifiers — `` `limit` `` | **works** | Any keyword is addressable as an attribute name. |
| Placing an entity in the trie | **runs** | `cmd/sdev1-addr`, end to end, no network. |
| **Writing a fact — `ASSERT` / `RETRACT`** | ❌ **not in the language** | The write *model* exists in Go (`command.Transaction`), but there is no statement. Decided, not built. |
| **`INSERT` / `UPDATE` / `DELETE`** | ❌ **will never exist** | The store appends. An update is an assertion, a delete a retraction, an erasure a destroyed key. |
| **Links — `relate`, `related`, references between entities** | ❌ **do not exist** | `Datom.Value` is untyped bytes, so a reference is indistinguishable from a string. Nothing can traverse. |
| **Taxonomies / hierarchies** | ❌ **do not exist** | They need links first. Once links are bitemporal datoms, "what did the hierarchy look like in March" falls out — but none of it is built. |
| **Joins, `AND`/`OR`, `ORDER BY`, `COUNT`** | ❌ | See the query guide's boundary table for which are decisions and which are gaps. |
| Storing anything on a disk | ❌ | No storage engine. This is the blocker under almost everything else. |
| Reading a stored fact back | ❌ | No query evaluator. |
| A server, a cluster, a network | ❌ | No transport. Everything is in-process. |

→ **[How to query, with the full grammar and worked examples](docs/QUERY-LANGUAGE.md)**
→ **[What is next, ordered by what blocks what](TODO.md)**

## Where this actually is

Read this before anything else on the page.

**The decisions are made and recorded. Most of the machinery that would execute
them is not built.** Twenty decision records are Accepted, each governs real Go
code, and 25 packages pass under the race detector. But there is no storage
engine, no network transport, and no query evaluator — so **you cannot yet start
a server and store a fact.**

| | |
|---|---|
| **Runs today** | 25 Go packages, 247 tests, race-clean. One binary, `sdev1-addr`. Every decision record's mechanism is decidable and tested in isolation. |
| **Does not exist** | A storage engine, a transport, a query evaluator, a node binary, a running cluster. |
| **Honestly measured** | 129 mutants killed across the corpus, 6 recorded as *survived* — those rows are kept rather than deleted, because a mutant that lived is the record of what the suite could not see. |

What that buys: every rule below is *checkable now*, with no cluster, and the
hard decisions — the ones that cannot be retrofitted once data exists — are
settled before there is data to migrate. What it costs: this is a design and a
proof of its parts, not a database you can run.

`docs/adr/README.md` says which half of each record is built.

---

## The shape of it

![Four layers: caller surfaces compile to one statement; routing resolves a key to a leaf; the leaf holds one fenced writer behind a published watermark; storage seals immutable erasure-coded segments](docs/diagrams/architecture.svg)

Four layers, each with one job:

1. **Surfaces** — the query language, agent tools, a filesystem. Every one of
   them compiles to a single query statement. None does its own reads.
2. **Routing** — longest-prefix over trie prefixes. A stale route is a redirect,
   never a wrong answer.
3. **The leaf** — one fenced writer, many readers, no locks between them.
4. **Storage** — immutable once sealed, erasure-coded, self-describing.

---

## The data model

### Datoms

A fact is a four-tuple: **entity, attribute, value, transaction** — a *datom*.
Nothing else is stored, and nothing is ever mutated. Changing an address means
asserting a new datom; deleting one means asserting a retraction. The log only
grows.

That choice is what makes every other property on this page possible. A store
that overwrites cannot answer a question about the past, and no amount of
auditing bolted on afterwards recovers what was overwritten.

### Two time axes

![Valid time on the horizontal axis, transaction time on the vertical, with the table of what each clause combination resolves to](docs/diagrams/bitemporal.svg)

Every datom sits at a point on two axes:

- **Valid time** — when the fact was true in the world.
- **Transaction time** — when this store learned it.

A backdated correction is low on the first axis and high on the second. With one
timestamp it would be indistinguishable from having always known — and *"what did
the report say when we ran it?"* would have no answer. That is the question every
audit asks, and it cannot be retrofitted.

The two are queried independently, and the defaults are one table:

| you wrote | transaction axis | valid time |
|-----------|------------------|------------|
| *(nothing)* | open | now |
| `AS OF t` | **open** | `t` |
| `TRANSACTION u` | `u` | now |
| `AS OF t TRANSACTION u` | `u` | `t` |

⚠ The second row is load-bearing. A lone instant binds valid time and leaves the
transaction axis **open**. Binding one value to both is what a reasonable
implementer writes by default, and it answers a different question than the one
asked.

### Transaction identity

A transaction is identified by a hybrid logical clock reading, the leaf it was
written on, and a sequence number. Together those give a **total order across the
whole cluster** without a global clock and without a coordinator. Wall-clock
skew cannot reorder two transactions, and two leaves writing at the same instant
never collide.

---

## The address space

![A tenant prefix followed by a hash, walked one byte per level down a 256-way trie](docs/diagrams/address-space.svg)

A key is 32 bytes: the **tenant** in the leading two, unhashed, then a hash of
the entity. Each level of the trie consumes exactly one byte.

**256 is the fan-out of every node — not a count of machines.** That distinction
is the whole decision. Fixing 256 as a shard *count* caps the system at 256
machines forever, and the cap is encoded in every key ever written, so it is
discovered only once migrating is expensive.

- **Fan-out is static.** 256 slots per node, fixed in the format.
- **Depth is dynamic.** It grows only under prefixes that hold data, one byte at
  a time, up to 32. A hot region is absorbed by splitting deeper.

The ceiling is 256³² leaves. The tenant prefix means a tenant owns a **contiguous
subtree**, which turns tenant deletion into a subtree drop, data residency into a
placement rule, and per-tenant durability, retention, compression and erasure-key
namespaces into subtree policy.

The topology is described over the levels **universe, planet, datacenter, rack,
server, disk**, held as a nested set, so "spread these copies across distinct
racks" is a range comparison rather than a tree walk.

---

## The write path

![The write path: append to the tail, replicate into memory across distinct power domains, and commit when the watermark advances](docs/diagrams/write-path.svg)

A write carries the writer's **lease epoch**. It is appended to the live tail —
complete, but still past the watermark, so no reader can reach it. It is then
replicated **into memory** on nodes in distinct power domains. When enough
distinct domains hold it, the watermark advances past it, and *that* is the
commit point. The caller hears "yes" before any disk has been touched.

Three things about that are deliberate:

**The watermark IS the commit point, not a second mechanism beside it.** The
watermark already makes an in-flight entry unreachable rather than half-visible,
so advancing it once the replicas hold the entry gives atomicity for readers with
nothing new added.

⚠ **N memory copies protect against *independent* failure, not *correlated*
failure.** Three nodes sharing one power feed lose everything unflushed together,
and nothing reports that this is the situation. So the replicas are **required**
to sit in distinct power domains rather than merely permitted to, and a write
that can only reach one domain is refused.

⚠ **Replication and flushing are different granularities** — per transaction and
per block. Conflating them holds a whole block's worth of writes unacknowledged
and spikes latency at every block boundary.

Two is a **durability** floor, not a consensus floor: two voting members give a
quorum of two, so a bare pair is *less* available than a single node. The
smallest genuinely available shape is two data replicas plus a witness.

## The read path

![Sealed segments are immutable; the live tail is bounded by a published watermark](docs/diagrams/read-path.svg)

**Readers and writers never block each other.** Most of that is free: sealed
segments are immutable, so reading them needs no coordination at all, and a
concurrent write lands at a *higher* transaction identifier and is simply not
part of an earlier snapshot.

The live tail is the only mutable part, and it is the whole decision. A writer
appends the entry in full and *then* release-stores the watermark. A reader
acquire-loads the watermark and reads the stable prefix before it.

⚠ **A partially written entry is unreachable, not guarded.** There is no lock to
contend on. Reverse the ordering — publish the watermark before the entry is
whole — and a reader can address half an entry, which no lock would have
prevented either, because the reader was *told* it was there. The window is
short, so it survives testing and appears under load.

A watermark means a stable **prefix**, so entries publish in order. Sealing
publishes by swapping an immutable manifest, so nobody ever observes a half-sealed
segment.

Writer-versus-writer is a different question, answered by the lease: one leaf has
one writer.

---

## Storage

![Compress, then encrypt, then erasure-code; the header records codec and cipher; every fragment carries a checksum](docs/diagrams/storage-pipeline.svg)

**The pipeline order is a consequence, not a preference.** Each step destroys the
property the next one needs: encrypting first would leave nothing compressible,
and coding first would mean compressing parity.

**Every block records how it was written** — codec, cipher, which stages ran, its
true length, and a checksum. Held only in configuration, changing a setting would
reinterpret every block already on disk. A constant that is safe as a *policy* is
fatal as a *format assumption*, and the fix is always the same: put it in the
header.

The checksum is verified **before** the codec runs, and decoding takes no
configuration — it reads what the header says.

### Erasure coding

A sealed block becomes a stripe of `k` data and `m` parity fragments, one per
failure domain, at `(k+m)/k` storage cost instead of 3×. Any `k` of them
reconstruct the block exactly.

⚠ **A code with `m` parity fragments corrects `m` fragments known to be MISSING,
but only ⌊m/2⌋ that are present and WRONG.** Locating a fault costs as much
redundancy as repairing it. So every fragment carries its own checksum and is
verified before decoding — which turns every *error* into an *erasure*, and
removes the case where reconstruction returns wrong bytes while reporting success.

The scheme travels with the stripe, so changing the configured one is safe at any
time.

### Erasure of a subject

Append-only storage cannot delete. Erasing a subject is the destruction of its
per-subject key — which reaches a coded stripe spread over ten domains, a replica
that has been offline for a month, and a backup on a shelf, without finding,
visiting or rewriting any of them.

⚠ **The ciphertext is permanent, and so is anything sitting beside it.** A key
handle *derived* from the subject would be an un-erasable identifier for that
subject, confirmable by anyone who could guess it. So handles are allocated random
bytes, and the mapping is destroyed with the key.

⚠ **A backup that holds the keystore alongside the data silently undoes every
erasure it contains.**

---

## The query language

One idea: **time is a clause, not a family of verbs.** There is no
`SELECT_HISTORY` and no `AS_OF_SELECT` — there is `SELECT`, and it may carry a
time qualifier.

**Every form the language accepts, in full.** There are three statements and one
standalone clause — that is the whole surface:

```sql
-- Read an entity. `*` is every attribute.
SELECT * FROM planet-7
SELECT mass, radius FROM planet-7
SELECT name FROM planet-7 WHERE class = 'terrestrial'
SELECT * FROM planet-7 WHERE mass >= -40.5

-- Time travel. Two independent axes.
SELECT * FROM planet-7 AS OF 1700000000                       -- valid time
SELECT * FROM planet-7 TRANSACTION 1700000500                 -- transaction time
SELECT * FROM planet-7 AS OF 1700000000 TRANSACTION 1700000500

-- Find subjects that resemble one. Metric and threshold are required.
MATCH SHAPE LIKE planet-7
  REQUIRE mass AS OF 1700000000, radius
  OPTIONAL nickname
  SIMILARITY jaccard >= 0.8

-- Full-text search. LIMIT is required.
SEARCH 'red dwarf' IN description LIMIT 20
SEARCH 'red dwarf' IN description, notes
  FACET BY class, discovered_by
  LIMIT 20
  AS OF 1700000000

-- Any keyword is addressable as an attribute name.
SELECT `limit`, `in` FROM planet-7

-- A storage policy for the NEXT write. Parsed standalone; no write statement
-- exists to carry it yet.
WITH COMPRESSION zstd
```

⚠ **There is no way to write a fact.** No `ASSERT`, no `RETRACT`, and by decision
never an `UPDATE` or a `DELETE`. ⚠ **And there are no links** — no `relate`, no
references between entities, so nothing can be traversed and there are no
taxonomies. Both are real gaps, not omissions from this list.

Parse any of the above today:

```bash
go test ./internal/core/ql/... -run TestSearchRoundTripsThroughTheAST -v
```

Because time is a clause, a **per-leg** qualifier costs nothing extra — match on
mass as it stood at one instant and nickname as it stood at another. Under a verb
family that would need a second grammar.

`REQUIRE` drops a row that does not match; `OPTIONAL` yields an **unbound** value
and keeps the row. Unbound is not the empty string — conflating them is how a
consumer reads "has no nickname" as "nickname is blank".

The metric and threshold are **required**, with no default: a default would make
every unqualified shape query reproducible only by whoever knew it.

**The language parses; nothing evaluates it yet.**

→ **[Full guide, grammar and tutorial: `docs/QUERY-LANGUAGE.md`](docs/QUERY-LANGUAGE.md)**

---

## Surfaces over the language

Two more ways in, and neither is a second way in. **Each compiles to a query
statement or it is not a surface** — a set of handlers that each reach storage
directly is a second query language with no grammar, a different time story per
handler, and the defaults table re-implemented once per site.

### Agent tools

An MCP surface whose caller is a model — the least forgiving caller there is,
because it has the tool list and nothing else. It cannot open the documentation,
read a changelog, or ask what an error meant.

- ⚠ **The tenant comes from the session, and a `tenant` argument is *ignored*,
  not rejected.** A rejection tells the caller the parameter exists. A model
  composes its next call from text it may have read out of this store, so the
  read itself is the injection vector.
- ⚠ **A refusal is a value, not an error.** A refusal in the error position is
  indistinguishable from a dropped connection, and the correct response to a
  dropped connection is to retry — so an agent retries it forever.
- **Time is an argument on every tool**, enforced at registration, which is what
  stops a `get_history` growing beside a `get`.
- **There is no `update` and no `delete`.** The harm is not at the API: a tool
  named for a verb this store does not have teaches a model a data model this
  system does not have, and the model then reasons about history and erasure
  wrongly — which nothing reports.

Read-only is a *consequence*, not a policy: the language has no write statement,
so there is nothing for a write tool to compile to.

### A filesystem

An entity is a directory, an attribute is a file, and an instant is a path prefix:

```
/e/planet-7/mass                        the value now
/.at/1700000000/e/planet-7/mass         the value as it stood then
```

Because the store is bitemporal and append-only, it is *already* a snapshotting
filesystem — so `cp -r /.at/<instant>/e /backup` is a point-in-time export, and
nothing involved had to learn anything about this system. That is the entire
reason to build it: a filesystem is a worse interface than the query language for
nearly everything, and it is the one interface tens of thousands of existing
programs already speak.

- ⚠ **Read-only, refused at `open` — never at `close`.** A program that opens for
  writing, buffers, and fails at `close(2)` has already lost the data, and many
  programs never check `close`. A directory opened for writing is `EROFS`, not
  `EISDIR`, because `EISDIR` would say that opening a *file* for writing would
  have worked.
- **Writes are not a missing feature.** POSIX cannot express that several
  attributes change together, and the entity is this store's transaction
  boundary. A writable projection would commit each `write(2)` as its own
  transaction and break that boundary *silently*, because every write succeeds.
- ⚠ **`mtime` is the datom's transaction time.** Callers read `stat` far more
  than contents — an mtime from the clock makes every file look modified on every
  pass, and every incremental backup silently becomes a full one.
- ⚠ **A shredded datom is `ENOENT`, identical to one that never existed.**
  `EACCES` would confirm the subject existed, which is an oracle anyone could
  query by guessing a name.

---

## Search and faceting

Everything above requires already knowing what you are looking for: `SELECT`
reads a **named** entity, and `MATCH SHAPE` needs a subject to resemble. Search
is how you ask without knowing, and faceting is how you ask what the matches look
like in aggregate.

One fact shapes the entire design.

⚠ **An ordinary inverted index silently undoes crypto-shredding.** Erasure works
because destroying a subject's key leaves ciphertext that nothing can read — an
argument that holds *only* while nothing readable sits beside it. An index is
extracted plaintext. Shred the key and the segments go dark while the index still
answers `term → subject-42`, which turns the fastest structure in the system into
a lookup for the person somebody asked to erase. And it cannot be fixed later:
every posting already written would already be in the clear, in every replica and
every backup of the index.

So **a posting is sealed with the subject's own key.** Shredding makes it
undecryptable everywhere at once — live index, replicas, backups — without
anything having to go and find them. Erasure reaches the index by exactly the
argument that makes it reach a coded stripe.

- ⚠ **A posting that does not decrypt is *absent*** — never an error, and never a
  withheld count. "3 results hidden" is the same existence oracle wearing a
  different hat, and `withheld++` is what anyone writes when a decrypt fails
  inside a loop. The function that filters them returns no second value, so it
  structurally cannot report one.
- **Deleting a shredded subject's postings is not the answer**, though it looks
  sufficient — it reintroduces a deletion that must find and visit every copy,
  and an offline replica keeps its own.
- **The index is derived and the log wins.** A result is a *candidate*, confirmed
  against the datoms before it is returned; an index fed by subscription is
  always behind. It is rebuildable from the log, so losing it is a performance
  event rather than a data-loss event.
- **Postings carry the transaction range they held over**, so search inherits the
  time clause. The price is that postings accumulate with history, not with data.
- ⚠ **A facet is exact or refused.** An unlabelled estimate is a lie, and a facet
  count is precisely the number people reconcile against a total. Over its
  declared bound it returns a named refusal and no partial counts.

The posting and facet model is built and proved against the real keystore. The
index itself, the ranking function and the `SEARCH` grammar are `BACKLOG.md` §27.

⚠ One limit is stated rather than solved: a sufficiently **rare term** is still
disclosive. This confines the leak from the subject to the term — a dictionary is
shared, and a rare enough term approximates an identifier. That is why
high-cardinality identifiers should not be indexed.

## How it fails, and how it recovers

![Three dispositions: recovers, unrecoverable by design, unrecoverable and open](docs/diagrams/failure-and-recovery.svg)

Every catalogued failure is one of exactly three things. There is no fourth,
because a fourth is how "we're looking into it" enters a catalogue and stops
anything being countable.

| Failure | Disposition | What happens |
|---|---|---|
| The writer for a leaf dies | **recovers** | A new grant issues at a higher epoch and never waits for the old holder. If the old one wakes, its writes are refused *at the resource*. |
| Two writers believe they hold a leaf | **recovers** | The epoch travels with the write; anything below the highest seen is refused. Checking "am I still leader?" and then writing has a window no amount of checking closes — so the check happens where the write lands. |
| Up to `m` fragments lost or corrupt | **recovers** | Verify the survivors, rebuild exactly. Corruption becomes an erasure rather than wrong bytes. |
| A route goes stale | **recovers** | A redirect carrying a newer epoch, bounded by a hop budget. The cache heals itself. |
| A node saturates | **recovers** | It withdraws from the queue, so work stops arriving. A hysteresis band keeps it from oscillating at the threshold. Read and write budgets are separate, so a read burst can never stop a leaf's writes. |
| A reader meets a half-written entry | **cannot happen** | It is past the watermark, so it is unreachable rather than guarded. |
| More than `m` fragments lost | **unrecoverable by design** | The block is destroyed. The read fails by name and returns nothing — a system that answered anyway would be inventing data. |
| A subject's key was destroyed | **unrecoverable by design** | That is erasure working. It reaches coded stripes, offline replicas and shelved backups without visiting any of them. |
| Every memory replica dies at once | **unrecoverable by design** | Unflushed acknowledged writes are lost. This is exactly why distinct *power* domains are required rather than permitted. |
| — | **unrecoverable and open** | **Zero entries today.** |

The third column is the point of keeping a catalogue at all: it is the half an
operator needs and the half nobody writes down. It held one entry until fencing
landed — a leaf whose writer died was readable forever and writable never. It was
found by injecting the fault, written down *before* it was fixed, and closed by
the same fault passing.

**Faults are injected from a seed**, so every failure is reproducible. An
unreproducible failure is a report rather than a bug: you cannot bisect it, prove
a fix, or tell a regression from bad luck.

→ **The living catalogue is [`docs/adr/FAILURES.md`](docs/adr/FAILURES.md).** If
it and this table ever disagree, the catalogue is right.

---

## Try it

Go 1.26 or newer. There is no server to start; the one binary makes the
addressing model visible.

```bash
git clone git@github.com:atvirokodosprendimai/sdev1.git
cd sdev1
go test ./... -race          # 25 packages, 247 tests
```

Place an entity in the trie and see why it landed there:

```bash
go run ./cmd/sdev1-addr \
  --topology testdata/topology/minimal.json \
  --entity planet-7 --tenant 7 \
  --spread-level rack --from srv-1
```

```
entity           planet-7
tenant           0007
tenant subtree   2:0007
tenant isolated  no — below depth 2 tenants share leaves
key              0007da710915d544ecffe4620fae9c7a261bf9428b88d42ca0f340377f63da62
depth            1
leaf             1:00

descent
  hop 1  level universe     byte 0x00  child 0

targets
  1. srv-3-d0
  2. srv-1-d0
  3. srv-1-d1
  4. srv-2-d0

spread across rack
  1. srv-3-d0
  2. srv-1-d0
  3. srv-1-d1
  4. srv-2-d0

nearest to srv-1
  1. srv-1-d0
  2. srv-1-d1
  3. srv-2-d0
  4. srv-3-d0
```

Run it twice; the answer is identical. That matters more than it looks — placement
has to be a pure function of the leaf and the topology map, or two clients send
the same key to different servers. Writing this README is what caught it not
being one; see [`TODO.md`](TODO.md) and ADR-001's T3 for what that cost.

Note the key: `0007` is the tenant, verbatim and unhashed, then the entity's
hash. And note what the command did *not* do — there was no network call and no
metadata service. Placement is computed from the topology map alone, which is the
claim the address space rests on, made visible in one command.

`--json` emits the same answer as JSON.

---

## Repository layout

```
cmd/sdev1-addr/           the one binary: where an entity lives, and why
docs/
  QUERY-LANGUAGE.md       the language, its grammar, and a tutorial
  diagrams/               the schematics on this page
  adr/                    the decision corpus — twenty Accepted records
    README.md             the index; says which half of each record is built
    FAILURES.md           the catalogue of what this does NOT survive
    BACKLOG.md            every deferred item, with a pointer back
internal/core/
  addr topology placement       the address space and where copies go
  hlc tx temporal               time: clock, identity, the two axes
  command ql                    the write path's reads, and the language
  segment erasure crypt         the byte format, coding, and erasure of a subject
  tail commit lease             the live tail, the commit point, fenced ownership
  routing prefetch admit        finding a leaf, reading ahead, shedding load
  subscribe observe chaos       streaming out, what is emitted, injected faults
  mcpsurface vfs                the agent and filesystem projections
  durability ports              policy over failure domains, and shared contracts
testdata/topology/        the fixture the binary runs against
```

Four direct dependencies: a CLI framework, a compression library, a Reed–Solomon
implementation, and their transitive support. Everything else is the standard
library.

---

## Where the decisions live

`docs/adr/` is the authoritative record. A decision that is not written there is
not a decision; it is a thing somebody happened to do.

Every record states its rejected alternatives — without them a decision reads as
arbitrary preference and the next reader reopens it — and names the paths it
governs, so `adr-context <path>` answers *"what already decided this, and which
approaches for it were already killed?"*

The four that cannot be retrofitted, because the choice is encoded in the data
itself:

| | |
|---|---|
| [ADR-001](docs/adr/ADR-001-address-space.md) | The key space is a 256-way trie of static fan-out and dynamic depth |
| [ADR-002](docs/adr/ADR-002-transaction-identity.md) | Transaction identity is a hybrid-logical-clock triple; the two time axes are queried independently |
| [ADR-003](docs/adr/ADR-003-transaction-boundary.md) | The entity is the transaction boundary — linearizable per entity, snapshot-isolated across them |
| [ADR-004](docs/adr/ADR-004-durability-policy.md) | Durability is a per-tier policy over a declared failure domain, with a refusal floor |

→ **[All twenty records, with what each one decides and why it could not wait](docs/adr/README.md)**

---

## Conventions

- **Write how it works, how it fails, and how it recovers.** A document that
  explains only the happy path has covered the easy half.
- **Describe mechanisms; do not attribute them.** Name a technique where the name
  *is* the thing — Reed–Solomon, a hybrid logical clock, a nested set. A reader
  needs to know how this system behaves, not how a different one does.
- **Numbers carry a date and a subject.** "Measured 2026-09-04 against a
  single-node checkout", never a bare figure. An undated measurement is
  unfalsifiable the moment the data changes.
- **A threshold is valid for a configuration, never in the abstract.**

---

## What is next

[`TODO.md`](TODO.md) — ordered, with what blocks what.

## License

See [LICENSE](LICENSE).
