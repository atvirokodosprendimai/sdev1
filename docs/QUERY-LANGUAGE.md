# The query language

One idea carries the whole design: **time is a clause, not a family of verbs.**

Everything else follows. There is no `READ_HISTORY`, no `AS_OF_READ`, no
`MATCH_AT`. There is `READ`, there is `MATCH SHAPE`, and either may carry a
time qualifier — including one per leg of a shape, which costs nothing extra
precisely because the qualifier is a clause rather than part of a verb's name.

> **Status.** `READ`, `ASSERT`, `RETRACT`, `SEARCH` and `TRAVERSE` **run** —
> against a session, and with `--dir` against a leaf on a disk that outlives the
> process. `MATCH SHAPE` parses and is refused by name, because a similarity
> metric chosen against no corpus is a number nobody has reason to believe
> (`docs/adr/BACKLOG.md` §20). Where a statement parses and does not run, this
> document says so rather than writing in a future tense that reads like a
> promise.

This page documents **every exported identifier** in the language package. That
is enforced rather than claimed — see [Documentation coverage](#documentation-coverage).

---

## Contents

**The language**
- [Statements](#statements)
- [Reading an entity](#reading-an-entity)
- [Filtering](#filtering)
- [Reading a table: `FROM [entity]`](#reading-a-table-from-entity)
- [Time travel](#time-travel)
- [The defaults table](#the-defaults-table)
- [Shape queries](#shape-queries)
- [Storage policy](#storage-policy)
- [Grammar](#grammar)
- [Lexical reference](#lexical-reference)

**The programmatic API**
- [Parsing](#parsing)
- [`Read` and `Predicate`](#read-and-predicate)
- [`TimeClause`](#timeclause)
- [`ShapeQuery`, `Leg` and `LegKind`](#shapequery-leg-and-legkind)
- [The result model: `Row` and `Binding`](#the-result-model-row-and-binding)
- [Storage policy: `PolicyClause` and `PolicyScope`](#storage-policy-policyclause-and-policyscope)
- [The lexer: `Lexer`, `Token` and `Kind`](#the-lexer-lexer-token-and-kind)
- [Errors](#errors)

**Boundaries**
- [What it deliberately cannot say](#what-it-deliberately-cannot-say)
- [Documentation coverage](#documentation-coverage)

---

# The language

## Statements

There are exactly three, and `Parse` accepts nothing else:

| Statement | Keyword it starts with | Compiles to |
|---|---|---|
| Read an entity's attributes | `READ` | `*Read` |
| Find resembling subjects | `MATCH` | `*ShapeQuery` |
| Find entities by their text | `SEARCH` | `*Search` |
| State that a fact held | `ASSERT` | `*Write` |
| State that a fact stopped holding | `RETRACT` | `*Write` |
| Walk the links out of an entity | `TRAVERSE` | `*Traverse` |

Anything else is refused at the first token with
`expected READ, MATCH, SEARCH, ASSERT, RETRACT or TRAVERSE`. A statement must
also be *complete* — trailing tokens are refused with `expected end of
statement`, rather than silently ignored:

```sql
READ * FROM planet-7 rubbish       -- refused: expected end of statement
```

⚠ **`SELECT` is reserved and refused by name.** It was the verb until ADR-034,
and it is kept in the keyword table for exactly one purpose: so that typing it
says what to type instead. It is not an alias — one verb has one spelling. The
refusal matches `errors.Is(err, ql.ErrSelectRenamed)`, so a caller can recognise
it without comparing message text:

```sql
SELECT * FROM planet-7             -- refused: SELECT was renamed to READ
```

A storage policy (`WITH COMPRESSION …`) is a **clause**, not a statement; it is
parsed separately. See [Storage policy](#storage-policy).

## Reading an entity

Project every attribute with `*`:

```sql
READ * FROM planet-7
```

Or name them, comma-separated:

```sql
READ mass FROM planet-7
READ mass, radius, discovered_by FROM planet-7
```

Keywords are case-insensitive, so `read * from planet-7` is the same
statement. Identifiers are case-**sensitive**.

⚠ `*` and a named list are the only two projections. There is no `READ` with an
empty list, and no way to mix them (`READ *, mass` is refused).

## Filtering

```sql
READ * FROM planet-7 WHERE mass > 1000
READ name FROM planet-7 WHERE class = 'terrestrial'
READ * FROM planet-7 WHERE class != "gas giant"
READ * FROM planet-7 WHERE mass >= -40.5
READ * FROM planet-7 WHERE status = active
```

Operators, all of them:

| | | | | | | |
|---|---|---|---|---|---|---|
| `=` | `==` | `!=` | `<` | `>` | `<=` | `>=` |

`=` and `==` are both accepted and parse identically; neither is normalised to
the other, so the tree records which one was written.

Values are a **number**, a **quoted string** (single or double), or a **bare
identifier**. Whether the value was written as a number is recorded on the tree
as `Predicate.ValueIsNumber` rather than guessed later — `mass > 1000` and
`mass > '1000'` are different questions, and an evaluator that had to infer which
one you meant would answer the wrong one some of the time.

⚠ **Exactly one predicate.** There is no `AND`, no `OR`, and no parentheses. See
[what it deliberately cannot say](#what-it-deliberately-cannot-say).

## Reading a table: `FROM [entity]`

Every read above names **one** entity, which means already knowing its
identifier. `FROM [e]` asks the other question: **which entities point AT `e`?**

```sql
READ ->name FROM [staff]
READ ->name, ->lastname FROM [staff] WHERE ->lastname = 'Adams'
READ * FROM [staff] LIMIT 20 OFFSET 40 AS OF 1700000000
```

An entity that N things point at **is a table**. Nothing new is stored to make
one: `ASSERT alice member = ->staff` already put `alice` in `staff`'s set,
because a reference is a datom like any other.

★ This is the one bounded way to ask "what is there". "Every entity" is
unbounded and needs routing nobody has built; "everything pointing at `staff`" is
bounded because `staff` is addressable.

**`->name` is a MEMBER's attribute.** Inside `FROM [e]` every attribute is
written with the marker, and outside one none of them are:

```sql
READ name FROM [staff]              -- refused: write ->name
READ ->name FROM staff              -- refused: write FROM [staff]
```

⚠ The two spellings are **not** synonyms. A bare `name` inside `FROM [staff]`
would have to mean *`staff`'s own* `name` — a join, which is not implemented.
Refusing it now is what lets a join be added later without changing what
already-written statements mean.

**A member missing anything the statement names is dropped.** Given
`READ ->name FROM [staff] WHERE ->lastname = 'Adams'`, a member is returned only
if it carries `name`, carries `lastname`, *and* the comparison holds. Missing
either attribute drops the member entirely — it is not returned with a hole.

⚠ This is deliberately the **opposite** of `OPTIONAL` in a
[shape query](#shape-queries), where an unmatched leg keeps the row with an
unbound binding. A shape query asks how much a subject *resembles* a pattern, so
a partial match is an answer. A table read asks which members *satisfy* a
condition, so it is not.

**`LIMIT` and `OFFSET` page over members**, not over rows — `LIMIT 20` is twenty
entities, however many attributes each contributes. Members are ordered by entity
name, and the page is taken **after** the drop, so page sizes are predictable.

⚠ **Paging is only coherent within one snapshot.** Across a moving present,
members shift between pages; pin the read with `AS OF` or `TRANSACTION` if that
matters. Paging is refused on a read of one entity, whose attributes are a shape
rather than a sequence:

```sql
READ * FROM planet-7 LIMIT 5        -- refused: nothing to page
```

## Absence: `WITHOUT`

`WHERE` asks what a value IS. `WITHOUT` asks for an attribute that is **not
there**:

```sql
READ ->name FROM [staff] WITHOUT ->thirdname
READ ->name FROM [staff] WHERE ->rank = 3 WITHOUT ->thirdname
READ ->name, ->lastname FROM [staff] WITHOUT ->thirdname, ->nickname
READ * FROM planet-7 WITHOUT radius
```

★ **It is a clause, not a predicate, and that is the whole design.** `WHERE`
holds exactly one comparison and the language has no `AND` — so if absence were a
predicate, "has rank 3 and lacks a thirdname" would need boolean composition.
Two clauses conjoin by being two clauses. No operator, no precedence, no
parentheses.

⚠ **Absence is relative to the snapshot, not to history.** `WITHOUT ->thirdname`
means *does not have one at the instant you asked about* — never *never had one*.
An attribute that was **retracted**, or whose validity interval has closed, is
absent; ask `AS OF` an earlier instant and it is present again. Being able to ask
the first question is what makes retraction mean anything.

⚠ **An excluded attribute is never also required.** A member missing a
*projected* or *compared* attribute is dropped; an attribute named in `WITHOUT` is
named in order to be missing, so it is exempt. Without that exemption the clause
would be unsatisfiable — and it would fail by returning nothing, which looks
exactly like a correct answer.


## Time travel

Two independent qualifiers, each optional, in this order:

```sql
READ * FROM planet-7 AS OF 1700000000
READ * FROM planet-7 TRANSACTION 1700000500
READ * FROM planet-7 AS OF 1700000000 TRANSACTION 1700000500
```

- **`AS OF t`** asks about **valid time** — when the fact was true in the world.
- **`TRANSACTION u`** asks about **transaction time** — when this store learned it.

They are different questions and the language keeps them apart. "What was the
address on the first of March?" is `AS OF`. "What did we *believe* the address
was when we ran the report last Tuesday?" needs `TRANSACTION` — and no single
timestamp can answer both.

Both take an **integer**. `AS OF 'yesterday'` is refused with
`expected an instant`; `TRANSACTION 1.5` is refused with
`expected a transaction reference`.

Order is fixed: `AS OF` before `TRANSACTION`. Writing them the other way round
leaves the `TRANSACTION` clause unconsumed and the statement is refused with
`expected end of statement`.

> A transaction is currently written as its clock reading, and parses into a
> `tx.TxID` whose `HLC.Wall` is that reading. A canonical textual form for a full
> transaction identifier is deferred until something produces one for a caller to
> copy.

![Two time axes, and what each clause combination resolves to](diagrams/bitemporal.svg)

### A worked example: the backdated correction

This is the thing bitemporality exists for, and you can run it:

```bash
go run ./cmd/sdev1-ql --clock 1000 \
  --statements "ASSERT planet-7 mass = 5972 VALID FROM 100" \
  --statements "ASSERT planet-7 mass = 6000 VALID FROM 100" \
  --statements "READ mass FROM planet-7 AS OF 150" \
  --statements "READ mass FROM planet-7 AS OF 150 TRANSACTION 1001"
```

```
ASSERT planet-7 mass = 5972 VALID FROM 100
  txn     1000.0@1:00#1
ASSERT planet-7 mass = 6000 VALID FROM 100
  txn     1001.0@1:00#2

READ mass FROM planet-7 AS OF 150
  planet-7   mass         6000

READ mass FROM planet-7 AS OF 150 TRANSACTION 1001
  planet-7   mass         5972
```

Both writes claim the *same* valid time — mass was 5972 from instant 100, then we
learned it was really 6000, also from instant 100. Nothing was overwritten.

- **`AS OF 150`** asks *what was true at 150?* → **6000**, the correction.
- **`AS OF 150 TRANSACTION 1001`** asks *what did we believe at 150, using only
  what we knew by transaction 1001?* → **5972**, the original.

That second question is the one every audit asks, and it is unanswerable in a
store that overwrites. `--clock 1000` makes transaction identifiers small and
reproducible so the example is readable; without it they are wall-clock
## Keeping what you write

By default `sdev1-ql` holds everything in memory and loses it on exit. `--dir`
keeps the leaf in a directory instead:

```bash
go run ./cmd/sdev1-ql --dir ./leaf --clock 1000 \
  --statements 'ASSERT planet-3 mass = "5.97e24"'

go run ./cmd/sdev1-ql --dir ./leaf --clock 5000 \
  --statements 'READ * FROM planet-3'
```

The second run prints `5.97e24`. It is a different process; nothing was carried
over in memory.

⚠ **What is written since the last seal is still in memory.** The run seals on the
way out, so an ordinary invocation keeps everything — but a process killed
mid-run loses what it had not sealed. That is a decision rather than an oversight:
an acknowledged write is held by replicas in distinct failure domains, not by a
disk, and making the disk the commit point would change the latency contract.

★ **The leaf's segment files have meaningless names, on purpose.** A read merges
them by the datoms' own transaction identifiers, so renaming, copying or restoring
them in any order gives the same answer. A name that sorted would be a name
something could come to depend on.
nanoseconds.

⚠ **A sharp edge worth knowing.** `TRANSACTION n` builds a bound whose leaf and
sequence are zero, which is the *lowest* identifier at instant `n` — so it
excludes transactions minted **at** `n`, not just after it. Copying an identifier
from the output and passing its wall value gives you the state **just before**
that write. Above, `TRANSACTION 1001` excludes the write at 1001, which is what
makes it show the pre-correction answer. To include a write, bound at the next
instant.

## The defaults table

The parse tree records **what you wrote**, unresolved. Defaults are applied in
one place — `TimeClause.Resolve` — and this is the whole table:

| you wrote | transaction axis | valid time |
|-----------|------------------|------------|
| *(nothing)* | open | now |
| `AS OF t` | **open** | `t` |
| `TRANSACTION u` | `u` | now |
| `AS OF t TRANSACTION u` | `u` | `t` |

⚠ **The second row is the load-bearing one.** A lone instant binds *business*
time and leaves the transaction axis **open** — it does not bind both. Binding
one value to both axes is what a reasonable implementer writes by default, and it
silently answers a different question than the one asked, so it is a stated rule
rather than a default nobody wrote down.

"Open" means no datom is excluded for having been recorded late.

Keeping "as written" separate from "resolved" is what makes this checkable: if
parsing applied defaults, there would be nothing left to compare the table
against.

## Shape queries

Find subjects resembling a subject, on attributes you name:

```sql
MATCH SHAPE LIKE planet-7
  REQUIRE mass, radius
  OPTIONAL nickname, discovered_by
  WITHOUT retired
  SIMILARITY jaccard >= 0.8
```

All three leg groups are optional and any may be omitted:

```sql
MATCH SHAPE LIKE planet-7 REQUIRE mass SIMILARITY jaccard >= 0.8
MATCH SHAPE LIKE planet-7 OPTIONAL nickname SIMILARITY jaccard > 0.5
MATCH SHAPE LIKE planet-7 WITHOUT retired SIMILARITY jaccard >= 0.9
MATCH SHAPE LIKE planet-7 SIMILARITY jaccard >= 0.9
```

They are written in the order `REQUIRE`, `OPTIONAL`, `WITHOUT`. Legs from all
three land in one `Legs` slice in written order, each tagged with its `LegKind`.

**Semantics of a leg:**

| Leg kind | Matched something | Matched nothing |
|---|---|---|
| `REQUIRE` | binds the value | **the row is dropped** |
| `OPTIONAL` | binds the value | binds **unbound**, and the row is **kept** |
| `WITHOUT` | **the row is dropped** | binds **nothing**, and the row is **kept** |

⚠ Unbound is not the empty string. Conflating them is how a consumer silently
reads "this subject has no nickname" as "this subject's nickname is blank". If
the optional case dropped the row too, `OPTIONAL` would mean the same thing as
`REQUIRE` — and the difference only shows on data where the leg is sometimes
absent, which is never the data anyone tests with.

⚠ **An excluded leg binds NOTHING — not `unbound`.** `Unbound` already means "an
optional leg matched nothing", so reusing it would make two opposite statements
render identically: one that the subject was asked for a value and had none, the
other that it was required to have none. ★ An excluded leg is a **filter**, and
its answer is already carried by the row existing at all. This means the row has
one binding per leg **that projects**, not one per leg.

★ An excluded leg carries its own time clause like any other, so
`WITHOUT nickname AS OF 1600000000` asks whether the subject lacked one *then*.
Time is a clause, which is why it attaches per leg — and a leg kind that could not
take one would be the first exception to that.

**The metric and threshold are required.** There is no default:

```sql
MATCH SHAPE LIKE planet-7 REQUIRE mass                      -- refused
MATCH SHAPE LIKE planet-7 REQUIRE mass SIMILARITY jaccard   -- refused
```

Both carry `ErrNoThreshold`'s text in the `Expected` field. A default threshold
would make every unqualified shape query reproducible only by whoever knows the
default, and the value would be a constant nobody wrote down.

The comparison accepts **`>=` or `>`** and nothing else — `<`, `<=` and `=` are
refused, because a similarity floor is the only useful form. The threshold is a
float, so `0.8`, `1`, and `.75` all parse.

Because time is a clause, **each leg may carry its own qualifier**:

```sql
MATCH SHAPE LIKE planet-7
  REQUIRE mass AS OF 1700000000, radius
  OPTIONAL nickname AS OF 1600000000 TRANSACTION 1650000000
  SIMILARITY jaccard >= 0.8
  AS OF 1700000000
```

That reads "match on mass as it stood at one instant and nickname as it stood at
another, over subjects as they stood at a third". Under a family of temporal
verbs this would need a second grammar; here it is the same clause applied in one
more position. A leg with no qualifier of its own carries a zero `TimeClause`.

## Writing facts

```sql
ASSERT planet-7 mass = 5972
ASSERT planet-7 class = 'terrestrial' VALID FROM 100
ASSERT planet-7 class = 'terrestrial' VALID FROM 100 TO 200
RETRACT planet-7 class = 'terrestrial' VALID FROM 500
```

One entity, one attribute, one value. **`VALID FROM t [TO u]` states when the
fact was true in the world.** Omit it and the fact holds from *this write's own
instant*, until further notice.

⚠ **There is no way to state when a fact was RECORDED, and that absence is
deliberate.** Valid time is a claim about the world, and backdating it is the
ordinary correct use of the axis. Transaction time is the record of when this
system was *told*, and it is the only thing that makes the store auditable — a
caller who could set it could claim to have known something earlier than they
did, and **no query could detect it**, because every query's evidence would be
the value that was forged. A `TRANSACTION` clause on a write is a parse error
carrying `ErrTransactionTimeIsNotYours`, not a clause that is quietly ignored.

⚠ **There is no `UPDATE` and no `DELETE`, and there never will be.** An update is
a new assertion at a later transaction; a deletion is a retraction; an erasure is
the destruction of a key. A CRUD verb would describe a data model this store does
not have — and everything a caller then inferred about history and erasure would
be wrong, silently.

⚠ **A retraction states when the fact stopped holding.** Omitting the clause
retracts from the write's own instant; retracting a fact *as if it had never been
true* has to be said explicitly, so an omission can never rewrite history by
accident.

The entity is the transaction boundary, so a write names exactly one — a second
entity does not parse, rather than failing at commit.

## Links and traversal

A value prefixed with `->` is a **reference** to another entity, not text:

```sql
ASSERT planet-7 orbits = ->star-1
ASSERT planet-7 note = 'star-1'
TRAVERSE planet-7 DEPTH 3
TRAVERSE planet-7 DEPTH 3 AS OF 1700000000
```

The first two write the same nine characters and mean different things. ⚠ **The
kind is how it was WRITTEN, never a guess from the bytes** — inferring would make
every value that spells an entity name an accidental edge, and the graph would
change whenever unrelated data did.

A link is an ordinary datom, so it is bitemporal, retractable and bound to one
entity without any of that being decided again. ★ **Which makes hierarchies
free**: a taxonomy is links, links are datoms, datoms are bitemporal, so "what
did this look like in March" is `TRAVERSE … AS OF …` rather than a feature.

⚠ **`DEPTH` is required.** An unbounded walk over a graph the caller does not
control is a scan they did not ask for. `ErrNoDepth` says so.

⚠ **A traversal carries ONE time clause, for the whole walk, and there is no
per-hop qualifier.** A shape query has one per leg and the symmetry is tempting —
but a per-hop clause would let you *ask* for March's root with today's children:
a tree in which every node is real, every edge existed at some point, and the
shape was never true at any moment. Making that sayable would turn a defect into
a feature, so it does not parse.

A cycle is reported rather than truncated, and a target that is missing,
retracted or **erased** is one indistinguishable answer — otherwise walking to an
entity would tell you whether it had been erased.

## Search and facets

```sql
SEARCH 'red dwarf' IN description LIMIT 20

SEARCH 'red dwarf' IN description, notes
  FACET BY class, discovered_by
  LIMIT 20
  AS OF 1700000000
```

- **`IN`** names the attributes to search. At least one is required.
- **`FACET BY`** is optional, and breaks the matches down by attribute value.
- **`LIMIT`** is **required** and must be positive.
- The time clause is the same one every other statement carries.

⚠ **The limit is required rather than defaulted, and a missing one is a parse
error.** A default would be a number nobody wrote down deciding how much of the
cluster a query touches — and search is the largest fan-out a single request can
cause. `ErrNoSearchLimit`'s text appears in the `Expected` field, so the error
says which part is wrong.

The query text is taken as written and analysed later by the same analyzer the
index used. Query syntax *inside* the text — phrases, negation, wildcards — is
not decided yet.

**What comes back is a set of CANDIDATES.** The index is derived and always
slightly behind the log, so a result must be confirmed against the datoms before
it is trusted. That confirmation is not built (`BACKLOG.md` §20), so today a
search over an in-memory index tells you what the index believes, which is not
quite the same as what is true.

⚠ **A shredded subject is absent from results and from facet counts**, because a
posting is sealed with the subject's own key and simply fails to decrypt. There
is no "withheld" count — that would be an oracle for the existence of erased
subjects. See `internal/core/search`.

## Storage policy

```sql
WITH COMPRESSION zstd
WITH COMPRESSION none
WITH COMPRESSION identity
```

Codec names are matched **case-insensitively** and normalised to lower case on
the clause. `identity` and `none` are synonyms for the same codec. An unknown
codec is refused with the known list in the error.

Two things about this clause are deliberate.

**It is currently standalone**, parsed by `ParsePolicyClause` rather than as part
of a statement. The statement that would *carry* it is a write, and no write
statement exists yet. Defining the clause now fixes what a policy *means*; which
statements accept it is decided when there is one to accept it.

**Its scope is new-writes-only, and there is no way to express anything else.**
Every block records the codec and cipher it was written with, so changing a
policy reinterprets nothing already stored. The language has no syntax for
re-encoding what exists, and the absence is enforced by there being no scope
value that means it — `PolicyScopes()` returns exactly one element.

## Grammar

As implemented, in EBNF:

```ebnf
statement   = read | shape | search | write | traverse ;

write       = ( "ASSERT" | "RETRACT" ) ident ident "=" writevalue
              [ "VALID" "FROM" number [ "TO" number ] ] ;
              (* no transaction clause exists, by decision *)

writevalue  = value | reference ;
reference   = "->" ident ;

traverse    = "TRAVERSE" ident "DEPTH" number timeclause ;
              (* ONE clause for the whole walk; no per-hop qualifier exists *)

read        = "READ" projection "FROM" source
              [ "WHERE" predicate ]
              [ "WITHOUT" attribute { "," attribute } ]
              [ page ]
              timeclause ;
              (* WHERE and WITHOUT conjoin; there is no operator between them,
                 which is how "has this and lacks that" avoids needing AND *)

source      = ident | "[" ident "]" ;
              (* `e` is one entity; `[e]` is the entities that point AT it *)

projection  = "*" | attribute { "," attribute } ;
attribute   = ident | "->" ident ;
              (* `->a` is a MEMBER's attribute and is required inside `[e]`;
                 a bare `a` is required outside one. Mixing them is refused. *)

page        = "LIMIT" number [ "OFFSET" number ] ;
              (* only on an inbound read; over MEMBERS, after the drop *)

predicate   = attribute operator value ;
operator    = "=" | "==" | "!=" | "<" | ">" | "<=" | ">=" ;
value       = number | string | ident ;

search      = "SEARCH" string "IN" attrlist
              [ "FACET" "BY" attrlist ]
              "LIMIT" number
              timeclause ;

attrlist    = ident { "," ident } ;

shape       = "MATCH" "SHAPE" "LIKE" ident
              [ "REQUIRE"  legs ]
              [ "OPTIONAL" legs ]
              [ "WITHOUT"  legs ]
              "SIMILARITY" ident ( ">=" | ">" ) number
              timeclause ;

legs        = leg { "," leg } ;
leg         = ident [ timeclause ] ;

timeclause  = [ "AS" "OF" number ] [ "TRANSACTION" number ] ;

policy      = "WITH" "COMPRESSION" codec ;          (* parsed standalone *)
codec       = "none" | "identity" | "zstd" ;
```

## Lexical reference

| Token class | `Kind` | Rule |
|---|---|---|
| end of input | `KindEOF` | Returned forever once the input is exhausted |
| identifier | `KindIdent` | Letters, digits, `_`, `-`, `:`; must start with a letter or `_`. Case-sensitive. Or **any text between backticks** — see quoting below |
| keyword | `KindKeyword` | One of the nineteen below, matched case-insensitively and normalised to UPPER CASE in `Token.Text` |
| number | `KindNumber` | Digits, an optional leading `-`, and any number of `.` |
| string | `KindString` | `'single'` or `"double"` quoted; `Token.Text` excludes the quotes |
| punctuation | `KindPunct` | Any other single rune, or one of the two-character operators `>=` `<=` `!=` `==` |

**The twenty-eight keywords**, all reserved:

```
READ    FROM  WHERE  AS  OF  TRANSACTION  MATCH
SHAPE   LIKE  REQUIRE  OPTIONAL  WITHOUT  SIMILARITY  WITH  COMPRESSION
SEARCH  IN  FACET  BY  LIMIT  OFFSET
ASSERT  RETRACT  VALID  TO
TRAVERSE  DEPTH
SELECT
```

⚠ `SELECT` names no statement. It is reserved so that typing the old verb is
refused by name rather than lexing as an attribute; see [Statements](#statements).

### Quoting an identifier

Keywords are reserved everywhere — but **backticks make any word an identifier**:

```sql
READ `limit`, `in` FROM planet-7
READ * FROM planet-7 WHERE `select` = 'yes'
```

The keyword table is never consulted inside backticks, and the quotes are not
part of the name — `` `limit` `` is the attribute named `limit`.

⚠ **This exists because adding a keyword is otherwise a silent breaking change.**
The last five words in the table above — `SEARCH`, `IN`, `FACET`, `BY`, `LIMIT` —
are ordinary English, and reserving them made any entity carrying an attribute of
that name unreadable, with no way to ask for it and no way to migrate off it.
Quoting landed in the same change as the keywords, and deliberately *before* them,
rather than being filed as a follow-up.

An earlier version of this page stated that no quoting mechanism existed. That
was true until the `SEARCH` statement needed five more keywords and made the
limitation someone's problem.

Other lexical facts worth knowing:

- **Whitespace** separates tokens and is otherwise ignored, so a statement may
  span any number of lines.
- **There are no comments.** `--` lexes as two punctuation tokens.
- **`-` is part of a number only when a digit follows it**, so `planet-7` is one
  identifier and `mass>-40` is three tokens.
- **An unterminated string is not a lex error.** The lexer returns what it has,
  positioned at the opening quote, and the parser produces the error with the
  position already correct.
- **`Token.Pos` is a byte offset**, and it is part of the contract rather than a
  diagnostic — see [Errors](#errors).

---

# The programmatic API

Everything below lives in `internal/core/ql`.

## Parsing

```go
func Parse(src string) (Statement, error)
```

`Statement` is a sealed interface — it has an unexported method, so the only
implementations are `*Read` and `*ShapeQuery`, and a type switch over the two
is exhaustive by construction:

```go
switch s := stmt.(type) {
case *Read:
	// …
case *ShapeQuery:
	// …
}
```

## `Read` and `Predicate`

```go
type Read struct {
	Attributes []string   // the projection; EMPTY means every attribute
	Entity     string     // what is being read, or whose referrers are
	Inbound    bool       // the source was written `FROM [Entity]`
	Where      *Predicate // nil when there was no WHERE
	Without    []string   // attributes the subject must NOT carry
	Page       Page       // the paging clause as written
	Time       TimeClause // as WRITTEN, before defaults
}
```

⚠ `Without` is a separate field rather than a kind of `Predicate` because it is a
separate CLAUSE. `Where` and `Without` conjoin, and neither implies the other.

⚠ `Attributes` being empty is how `READ *` is represented. There is no separate
"star" flag, so a consumer must treat empty as *all* rather than as *none*.

★ `Entity` holds the identifier WITHOUT its brackets, and `Inbound` carries the
difference. `FROM staff` and `FROM [staff]` address the same entity and ask
different questions, so the name is stored once and the question is a flag.

⚠ The `->` marker is grammar and never part of an attribute name: `->name`
projects the attribute `name`. Storing the marker would make `->name` and `name`
different attributes in the store, which is a data model invented by a parser.

```go
type Page struct {
	Limit  int64 // the maximum number of MEMBERS returned
	Offset int64 // how many surviving members to skip first
	Has    bool  // whether a clause was written at all
}
```

⚠ `Has` is why `LIMIT 0` and no clause at all are distinguishable. They are
opposite requests — no rows, and all of them — so a `Page` without the flag would
make the emptier one the default for every statement that omits the clause.

```go
var ErrJoinNotSupported error
```

Returned when an attribute's `->` marker disagrees with what `FROM` named. Inside
`FROM [e]` every attribute belongs to a member and is written `->a`; a bare `a`
would mean `e`'s own attribute, which is a join and is not implemented. ★ It is
refused rather than accepted as a synonym so that a join can be added later
without changing what already-written statements mean.

```go
type Predicate struct {
	Attribute     string
	Op            string // one of = == != < > <= >=, exactly as written
	Value         string // the literal, with quotes already stripped
	ValueIsNumber bool   // whether it was written as a number
}
```

## `TimeClause`

```go
type TimeClause struct {
	ValidAt *int64    // from AS OF t, or nil
	AsOf    *tx.TxID  // from TRANSACTION u, or nil
}

func (c TimeClause) Resolve(now int64) temporal.Query
```

Both fields are pointers so "not supplied" is representable and cannot be
confused with a zero value — an instant of zero is a legitimate question.

⚠ The two fields have **different types on purpose**. The defect this guards
against is passing one value into both axes; when both are plain instants that is
a one-character mistake, and when one is an instant and the other a transaction
identifier the compiler refuses it. The type system carries part of a rule that
would otherwise rest entirely on review.

`Resolve` applies [the defaults table](#the-defaults-table) by forwarding to the
package that owns what time means. It decides nothing itself, and contains no
branch — two implementations of a four-row table would drift, and the drift is
invisible until a query returns the wrong history.

## `Write` and `WriteOp`

```go
type Write struct {
	Op            WriteOp
	Entity        string
	Attribute     string
	Value         string // the literal, quotes stripped
	ValueIsNumber bool
	From          *int64 // from VALID FROM t, or nil
	To            *int64 // from TO u, or nil for an open interval
}

func (w *Write) Interval(now int64) temporal.Interval
```

```go
type WriteOp int

const (
	OpUnset   WriteOp = iota // the zero value, never valid
	OpAssert                 // the fact held
	OpRetract                // the fact stopped holding
)

func WriteOps() []WriteOp     // exactly two
func (o WriteOp) String() string
```

`OpUnset` exists so a zero-valued `Write` is detectably wrong rather than
silently behaving like an assertion — which is the more dangerous of the two to
get by accident.

`WriteOps` returns the closed pair. It is exported so a caller enumerating the
verbs walks the language's own list rather than one kept beside it.

⚠ `Interval(now)` resolves an omitted `VALID` clause to `[now, Forever)` — the
write's own instant — and **not** to `[0, Forever)`. Defaulting to zero would
silently claim every fact had been true since the beginning of time, and nothing
about the resulting datom would look unusual.

```go
var ErrTransactionTimeIsNotYours = errors.New(
    "ql: a write states when a fact was TRUE, never when it was recorded; " +
    "transaction time is assigned by the system")
```

Its text is embedded in the `Expected` field of the `ParseError` a write with a
`TRANSACTION` clause produces, so the caller gets the position and the reason
together — rather than an "unexpected token" they will try harder to work around.

## `Traverse`

```go
type Traverse struct {
	Root  string     // the entity to walk from
	Depth int        // always positive — see ErrNoDepth
	Time  TimeClause // ONE clause, applied at every hop
}
```

```go
var ErrNoDepth = errors.New("ql: a traversal needs a positive DEPTH")

const RefMarker = "->"
```

⚠ `Traverse` has no per-hop time field, and none may be added. See
[Links and traversal](#links-and-traversal) for why that absence is the point.

`RefMarker` is exported so a tool that generates statements uses the same marker
the lexer does rather than hard-coding it. A write's reference-ness reaches the
tree as `Write.ValueIsReference`.

## `Search`

```go
type Search struct {
	Query      string     // the text, exactly as written; analysed later
	Attributes []string   // the attributes searched; never empty
	Facets     []string   // the attributes to break matches down by, or nil
	Limit      int        // always positive — see ErrNoSearchLimit
	Time       TimeClause // the same clause every statement carries
}
```

```go
var ErrNoSearchLimit = errors.New("ql: a search needs a positive LIMIT")
```

`ErrNoSearchLimit` is not returned directly; its text is embedded in the
`Expected` field of the `ParseError` a limitless search produces, so the caller
gets the position and the reason together.

```go
const IdentQuote = '`'
```

`IdentQuote` is the rune that makes any word an identifier. It is exported so a
tool that generates or escapes queries uses the same character the lexer does,
rather than hard-coding a backtick and drifting if it ever changes.

## `ShapeQuery`, `Leg` and `LegKind`

```go
type ShapeQuery struct {
	Subject   string
	Legs      []Leg
	Metric    string
	Threshold float64
	Time      TimeClause
}

type Leg struct {
	Attribute string
	Kind      LegKind
	Time      TimeClause // this leg's own qualifier
}
```

```go
type LegKind int

const (
	LegKindUnset LegKind = iota // the zero value, never valid
	LegRequired                 // matches nothing → the row is dropped
	LegOptional                 // matches nothing → unbound, row kept
	LegExcluded                 // MATCHES → the row is dropped; binds nothing
)

func (k LegKind) String() string  // "unset" | "required" | "optional" | "excluded"
```

`LegKindUnset` exists so that a zero-valued `Leg` is detectably wrong rather than
silently behaving like one of the real kinds.

⚠ `LegExcluded` is the mirror of `LegRequired` and contributes **no binding**. A
consumer indexing `Row.Bindings` positionally against `ShapeQuery.Legs` would be
wrong: there is one binding per leg that projects.

## The result model: `Row` and `Binding`

This is the shape an evaluator must produce. It is decided here, as pure
functions, so the semantics are settled before any evaluator exists.

```go
type Binding struct {
	Attribute string
	// value and bound are unexported: a binding can only be made by Bound or
	// Unbound, so "no value" cannot be constructed by accident.
}

func Bound(attribute, value string) Binding
func Unbound(attribute string) Binding

func (b Binding) IsBound() bool
func (b Binding) Value() (string, bool)
func (b Binding) String() string   // `attr="value"` or `attr=<unbound>`
```

An **inbound read** returns one group of rows per surviving member, members in
entity-name order and each member's attributes sorted within it. `LIMIT` counts
members, so a page of 20 members carrying three attributes each is 60 rows.

⚠ A reader that cannot say what points at an entity **refuses** with
`eval.ErrNoInboundIndex` rather than returning nothing. "Nothing points at this"
and "I cannot tell you what points at this" are different answers, and returning
the first for the second is the defect the evaluator exists to prevent.

```go
type Row struct {
	Subject  string
	Bindings []Binding // one per leg, in the order the legs were written
}

func (r Row) Get(attribute string) (Binding, bool)
```

```go
func BuildRow(subject string, legs []Leg, matched map[string]string) (Row, bool)
```

`BuildRow` **is** the match semantics, written as a pure function so the rule is
decidable with no storage engine: an evaluator supplies `matched`, and this
decides what the row looks like and whether it survives.

| Leg kind | In `matched` | Result |
|---|---|---|
| any | yes | `Bound` binding appended |
| `LegRequired` | no | returns `(Row{}, false)` — **the row is dropped** |
| `LegOptional` | no | `Unbound` binding appended, row kept |

The second return is `false` exactly when a required leg matched nothing.

## Storage policy: `PolicyClause` and `PolicyScope`

```go
func ParsePolicyClause(src string) (PolicyClause, error)

type PolicyClause struct {
	Codec segment.CodecID // resolved against the segment format's registry
	Name  string          // the codec as written, lower-cased, for diagnostics
	Scope PolicyScope     // always PolicyNewWritesOnly
}
```

```go
type PolicyScope int

const PolicyNewWritesOnly PolicyScope = 1

func PolicyScopes() []PolicyScope     // exactly one element
func (s PolicyScope) String() string  // "new writes only" | "unset"
```

⚠ `Scope` is a field on the clause even though it can only hold one value. That
is deliberate: a reader of the tree *sees* the limit rather than having to know
it, and the day a second scope is proposed, the place it would have to go already
exists and is already named.

`Codec` resolves to an identifier the segment format already understands, so the
language adds no second codec registry — one registry, one set of names.

## The lexer: `Lexer`, `Token` and `Kind`

The lexer is exported so a tool can highlight or inspect a statement without
parsing it.

```go
func NewLexer(src string) *Lexer

func (l *Lexer) Next() Token     // one token; KindEOF forever at the end
func (l *Lexer) Tokens() []Token // the whole input, ending with a KindEOF token
```

```go
type Token struct {
	Kind Kind
	Text string // keywords are upper-cased; strings have quotes stripped
	Pos  int    // byte offset
}

func (t Token) String() string   // e.g. `keyword "READ"`, or `end of input`
```

```go
type Kind int

const (
	KindEOF Kind = iota
	KindIdent
	KindKeyword
	KindNumber
	KindString
	KindPunct
)

func (k Kind) String() string
```

`Kind.String` returns the words used in error messages — `end of input`,
`identifier`, `keyword`, `number`, `string`, `punctuation` — so a `ParseError`
reads in the same vocabulary a caller sees here.

## Errors

```go
type ParseError struct {
	Pos      int    // byte offset of the token that could not be accepted
	Found    string // what was there
	Expected string // what would have been accepted
}

func (e *ParseError) Error() string
```

```
ql: at byte 24: found keyword "FROM", expected an attribute name
```

The position is part of the contract, not a diagnostic. A parser that answers
"syntax error" has failed at its actual job, which for a language is telling the
caller what to write instead.

```go
var ErrNoThreshold = errors.New("ql: a shape query needs a metric and a threshold")
```

`ErrNoThreshold` is not returned directly — its text is embedded in the
`Expected` field of the `ParseError` a threshold-less shape query produces, so
the caller gets the position *and* the reason in one value.

---

# Boundaries

## What it deliberately cannot say

Listing these is part of the design rather than an apology — each is a decision
with a reason, and none is a gap someone forgot.

| Not expressible | Why |
|---|---|
| `AND` / `OR` / parentheses in `WHERE` | One predicate is what the evaluator will first have to satisfy. Compound predicates are a language extension to make once there is something to run them against, rather than a grammar to guess at now. |
| More than one entity per `READ` | The entity is the transaction boundary. A cross-entity read is a read over a snapshot, and what a snapshot spans is a decision the storage engine has to make first. |
| Joins | Same reason, one step further out. |
| Enumerating entities | The language reads a **named** entity. An unbounded listing over a planetary key space is not something a single result can return, so the shape of that answer is a real decision rather than a missing keyword. |
| Ordering or limiting a `READ` | There is nothing to order by. A `READ` reads one named entity, so its result has no ranking, and `ORDER BY` before an evaluator exists would be guessing at a cost model. ⚠ADR-021 lifts this for `SEARCH`, where ranking exists and an unranked, unlimited search is a full scan with extra steps. The statement itself is `pending` — `BACKLOG.md` §27. |
| Aggregation — `COUNT`, `SUM` | Not in the language. ⚠The one counting a caller can ask for is a FACET over a search result, decided in ADR-021: exact or refused, never estimated. Also `pending` on §27. |
| ~~Full-text search~~ | **`SEARCH` now parses** and runs against an in-memory index with deterministic ranking. What is still missing is a PERSISTED index and confirmation of candidates against the datoms (`BACKLOG.md` §27 and §20), so a result today reflects what the index believes rather than what is true. |
| Query syntax inside the search text | Phrases, negation and wildcards are undecided. The text is taken as written and analysed by the same analyzer the index used. |
| ~~Writes — `ASSERT` / `RETRACT`~~ | **Now in the language**, and runnable against an in-memory session. See [Writing facts](#writing-facts). Durable storage is still `BACKLOG.md` §12. |
| Stating when a fact was RECORDED | Permanently absent. Transaction time is assigned by the system; a caller who could forge it would make every historical answer a claim rather than a record. |
| `UPDATE` or `DELETE`, ever | The store appends. A retraction is an assertion, and erasure is the destruction of a key. A verb implying in-place mutation would describe a data model this system does not have. |
| ~~Quoting a keyword as an identifier~~ | **No longer true.** Backticks make any word an identifier — see [Quoting an identifier](#quoting-an-identifier). It was a real limitation until the `SEARCH` statement needed five more keywords and made it someone's problem. |
| Re-encoding existing data via a policy | Every block records how it was written. A policy sets what the **next** write produces; there is no scope value meaning "and rewrite what exists". |
| Naming a similarity floor with `<` or `=` | A similarity threshold is a floor. Accepting `<` would let a query ask for *dissimilar* subjects through a keyword that says the opposite. |

## Documentation coverage

Every exported identifier in the language package — 15 types, 7 functions, 10
constants and 1 error variable — appears somewhere on this page, inside a code
block or as a `backticked` name.

That is checked rather than asserted. `TestQueryLanguageDocIsComplete` in
`internal/core/ql/doccoverage_test.go` parses the package's own source for
exported top-level declarations, extracts the code spans from this file, and
fails naming anything missing.

⚠ It searches only code spans, never prose. An earlier guard in this repository
matched a symbol in a comment that explained why *not* to use it, and a check
that fires on prose gets switched off — after which it protects nothing.

So a new exported identifier makes the suite go red until it is documented here.
This page cannot fall behind the language without someone deciding to let it.

---

**The decision behind this language** is `docs/adr/ADR-011-query-language.md`,
with the two time axes in `ADR-002` and the append-only model in `ADR-003`. The
surfaces that compile *to* this language are `ADR-013` (agent tools) and
`ADR-014` (the filesystem).

The runnable example from this page lives in
`internal/core/ql/example_readme_test.go`, with its output pinned as the expected
result — a code block nothing executes is a claim, not a sample.
