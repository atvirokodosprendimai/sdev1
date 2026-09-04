# The query language

One idea carries the whole design: **time is a clause, not a family of verbs.**

Everything else follows. There is no `SELECT_HISTORY`, no `AS_OF_SELECT`, no
`MATCH_AT`. There is `SELECT`, there is `MATCH SHAPE`, and either of them may
carry a time qualifier — including one qualifier per leg of a shape, which costs
nothing extra precisely because the qualifier is a clause rather than part of a
verb's name.

> **Status.** The language **parses**. Nothing **evaluates** it yet, because
> there is no storage engine behind it (`docs/adr/BACKLOG.md` §20 and §12). Every
> statement on this page is real — you can parse it today and inspect the tree —
> but none of them returns rows. Where that changes what you should expect, this
> document says so rather than writing in a future tense that reads like a
> promise.

---

## Contents

- [Reading an entity](#reading-an-entity)
- [Filtering](#filtering)
- [Time travel](#time-travel)
- [The two axes, and the table that resolves them](#the-two-axes-and-the-table-that-resolves-them)
- [Shape queries](#shape-queries)
- [Storage policy](#storage-policy)
- [Grammar](#grammar)
- [Errors](#errors)
- [What it deliberately cannot say](#what-it-deliberately-cannot-say)
- [Parsing one yourself](#parsing-one-yourself)

---

## Reading an entity

Project every attribute:

```sql
SELECT * FROM planet-7
```

Or name the ones you want:

```sql
SELECT mass, radius FROM planet-7
```

Keywords are case-insensitive, so `select * from planet-7` is the same
statement. Identifiers are case-**sensitive** and may contain letters, digits,
`_`, `-` and `:` — which is why `planet-7` is one name rather than a subtraction,
and why a leaf identifier like `2:0007` can be written literally.

## Filtering

```sql
SELECT * FROM planet-7 WHERE mass > 1000
SELECT name FROM planet-7 WHERE class = 'terrestrial'
SELECT * FROM planet-7 WHERE class != "gas giant"
```

Comparison operators are `=`, `==`, `!=`, `<`, `>`, `<=`, `>=`. Values are
numbers, single- or double-quoted strings, or bare identifiers.

Whether a value was written as a number is **recorded on the parse tree** rather
than guessed later. `mass > 1000` and `mass > '1000'` are different questions,
and an evaluator that had to infer which one you meant would answer the wrong one
some of the time.

One `WHERE` predicate is supported. Conjunction, disjunction and grouping are not
in the language yet — see [what it deliberately cannot say](#what-it-deliberately-cannot-say).

## Time travel

Two independent qualifiers, each optional, in this order:

```sql
SELECT * FROM planet-7 AS OF 1700000000
SELECT * FROM planet-7 TRANSACTION 1700000500
SELECT * FROM planet-7 AS OF 1700000000 TRANSACTION 1700000500
```

- **`AS OF t`** asks about **valid time** — when the fact was true in the world.
- **`TRANSACTION u`** asks about **transaction time** — when this store learned it.

They are different questions and the language keeps them apart. "What was the
address on the first of March?" is `AS OF`. "What did we *believe* the address was
when we ran the report last Tuesday?" needs `TRANSACTION` — and no single
timestamp can answer both.

> A transaction is currently written as its clock reading. A canonical textual
> form for a full transaction identifier is deferred until something produces one
> for a caller to copy.

![Two time axes, and what each clause combination resolves to](diagrams/bitemporal.svg)

## The two axes, and the table that resolves them

The parse tree records **what you wrote**, unresolved. Defaults are applied in one
place, and this is the whole table:

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

Keeping "as written" separate from "resolved" is what makes this checkable at
all: if parsing applied defaults, there would be nothing left to compare the
table against.

## Shape queries

Find subjects that resemble a subject, on attributes you name:

```sql
MATCH SHAPE LIKE planet-7
  REQUIRE mass, radius
  OPTIONAL nickname, discovered_by
  SIMILARITY jaccard >= 0.8
```

- **`REQUIRE`** — a leg that matches nothing **drops the row**.
- **`OPTIONAL`** — a leg that matches nothing yields an **unbound** value and the
  row is **returned**.

⚠ Unbound is not the empty string. Conflating them is how a consumer silently
reads "this subject has no nickname" as "this subject's nickname is blank". If
the optional case dropped the row too, `OPTIONAL` would mean the same thing as
`REQUIRE` — and the difference only shows on data where the leg is sometimes
absent, which is never the data anyone tests with.

**The metric and threshold are required.** There is no default:

```sql
MATCH SHAPE LIKE planet-7 REQUIRE mass SIMILARITY jaccard >= 0.8   -- fine
MATCH SHAPE LIKE planet-7 REQUIRE mass                             -- refused
```

A default threshold would make every unqualified shape query reproducible only by
whoever knows the default, and the value would be a constant nobody wrote down.
Being refused at parse time is the cheaper failure.

Because time is a clause, **each leg may carry its own qualifier**:

```sql
MATCH SHAPE LIKE planet-7
  REQUIRE mass AS OF 1700000000, radius
  OPTIONAL nickname AS OF 1600000000
  SIMILARITY jaccard >= 0.8
  AS OF 1700000000
```

That reads "match on mass as it stood at one instant and nickname as it stood at
another". Under a family of temporal verbs this would need a second grammar; here
it is the same clause applied in one more position.

## Storage policy

```sql
WITH COMPRESSION zstd
```

Codecs: `none`, `identity` (a synonym for `none`), `zstd`.

Two things about this clause are deliberate.

**It is currently standalone.** The statement that would *carry* it is a write,
and no write statement exists yet. Defining the clause now fixes what a policy
*means*; which statements accept it is decided when there is one to accept it.

**Its scope is `new writes only`, and there is no way to express anything else.**
Every block records the codec and cipher it was written with, so changing a policy
reinterprets nothing already stored. The language has no syntax for re-encoding
what exists, and the absence is enforced by there being no scope value that means
it.

## Grammar

As implemented, in EBNF:

```ebnf
statement   = select | shape ;

select      = "SELECT" projection "FROM" ident
              [ "WHERE" predicate ]
              timeclause ;

projection  = "*" | ident { "," ident } ;

predicate   = ident operator value ;
operator    = "=" | "==" | "!=" | "<" | ">" | "<=" | ">=" ;
value       = number | string | ident ;

shape       = "MATCH" "SHAPE" "LIKE" ident
              [ "REQUIRE"  legs ]
              [ "OPTIONAL" legs ]
              "SIMILARITY" ident ( ">=" | ">" ) number
              timeclause ;

legs        = leg { "," leg } ;
leg         = ident [ timeclause ] ;

timeclause  = [ "AS" "OF" number ] [ "TRANSACTION" number ] ;

policy      = "WITH" "COMPRESSION" codec ;          (* parsed standalone *)
codec       = "none" | "identity" | "zstd" ;
```

Lexical rules:

| | |
|---|---|
| **keywords** | `SELECT FROM WHERE AS OF TRANSACTION MATCH SHAPE LIKE REQUIRE OPTIONAL SIMILARITY WITH COMPRESSION` — matched case-insensitively |
| **identifiers** | letters, digits, `_`, `-`, `:`; case-sensitive |
| **strings** | `'single'` or `"double"` quoted |
| **numbers** | digits, optionally leading `-`, optionally containing `.` |
| **comments** | none |

⚠ **Keywords are reserved everywhere.** An attribute named `like` or `shape`
lexes as a keyword and will not parse as a name, and there is no quoting
mechanism to escape one. That is a real limitation rather than a subtlety — if
your data uses one of those fourteen words as an attribute name, the language
cannot currently address it.

## Errors

A parse failure is a value, not a string:

```go
type ParseError struct {
    Pos      int    // byte offset of the token that could not be accepted
    Found    string // what was there
    Expected string // what would have been accepted
}
```

```
ql: at byte 24: found keyword "FROM", expected an attribute name
```

The position is part of the contract, not a diagnostic. A parser that answers
"syntax error" has failed at its actual job, which for a language is telling the
caller what to write instead.

## What it deliberately cannot say

Listing these is part of the design rather than an apology — each one is a
decision with a reason, and none of them is a gap someone forgot.

| Not expressible | Why |
|---|---|
| `AND` / `OR` / parentheses in `WHERE` | One predicate is what the evaluator will first have to satisfy. Compound predicates are a language extension to make once there is something to run them against, rather than a grammar to guess at now. |
| Joins across entities | The entity is the transaction boundary. A cross-entity join is a read over a snapshot, and what a snapshot spans is a decision the storage engine has to make first. |
| Enumerating entities | The language reads a **named** entity. An unbounded listing over a planetary key space is not something a single result can return, so the shape of that answer is a real decision rather than a missing keyword. |
| Writes — `ASSERT` / `RETRACT` | Nothing evaluates yet, so a write statement would be syntax with no semantics. When it lands, the append-only model means there is no `UPDATE` and no `DELETE`. |
| `UPDATE` or `DELETE`, ever | The store appends. A retraction is an assertion, and erasure is the destruction of a key. A verb implying in-place mutation would describe a data model this system does not have. |
| Re-encoding existing data via a policy | Every block records how it was written. A policy sets what the **next** write produces; there is no scope value meaning "and rewrite what exists". |

## Parsing one yourself

There is no CLI for the language yet — `sdev1-addr` is the only binary, and it
covers addressing rather than querying. From Go:

```go
stmt, err := ql.Parse("SELECT mass FROM planet-7 AS OF 1700000000")
if err != nil {
	fmt.Println("refused:", err)
	return
}

sel := stmt.(*ql.Select)
fmt.Println("entity:    ", sel.Entity)
fmt.Println("attributes:", sel.Attributes)

// As WRITTEN — the transaction axis is nil, meaning open.
fmt.Println("valid at:  ", *sel.Time.ValidAt)
fmt.Println("as of txn: ", sel.Time.AsOf)

// Resolved through the one implementation of the defaults table.
resolved := sel.Time.Resolve(1700009999)
fmt.Println("resolved:  ", *resolved.ValidAt, resolved.AsOf)
```

```
entity:     planet-7
attributes: [mass]
valid at:   1700000000
as of txn:  <nil>
resolved:   1700000000 <nil>
```

`Resolve` calls into the package that owns what time means and decides nothing
itself. Two implementations of a four-row table would drift, and the drift is
invisible until a query returns the wrong history.

> **This snippet is a test.** It lives in the repository as
> `internal/core/ql/example_readme_test.go` with the output above pinned as its
> expected result, so it cannot drift from the API without the suite failing. A
> code block nothing executes is a claim, not a sample.
>
> `ql` is an `internal/` package, so it can only be imported from inside this
> module — which is also why the example is a test here rather than a `main` you
> paste elsewhere.

---

**The decision behind this language** is `docs/adr/ADR-011-query-language.md`,
with the two time axes in `ADR-002` and the append-only model in `ADR-003`. The
surfaces that compile *to* this language are `ADR-013` (agent tools) and
`ADR-014` (the filesystem).
