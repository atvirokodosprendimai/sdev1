// Package temporal decides whether a datom is visible to a query, on two
// independent time axes.
//
// # The two axes
//
// BUSINESS time is when a fact was true in the world. The writer supplies it as
// a validity interval. TRANSACTION time is when the system recorded the fact;
// the writer never supplies it. They are independent, and the whole value of
// storing both is that they can DISAGREE — a fact recorded today may have been
// true since last year, and a correction recorded today may replace what was
// recorded last year about last year.
//
// # The rule that is easy to get wrong
//
// A caller supplying ONE instant is asking a business-time question. That
// instant binds [Query.ValidAt] and leaves [Query.AsOf] open.
//
// Binding one instant to both axes is the mistake, and it is the mistake a
// reasonable implementer makes, because "as of that moment" sounds like it
// should constrain everything. It does not: a backdated write commits NOW and
// is valid FROM THE PAST, so constraining the transaction axis by a past
// instant excludes the very write the query is asking about. The query returns
// nothing and looks correct.
//
// # How it fails, and how it recovers
//
// The failure is silent in both directions. There is no error, no warning and
// no partial result — a query that should return a backdated fact simply
// returns nothing, and a test suite that never makes the two axes disagree
// stays green over it. A sibling project shipped exactly this past roughly 140
// tests including a race detector, because every one of its tests happened to
// write with validity beginning at commit time, so the two parameters were
// never actually different in any test.
//
// Recovery is cheap when caught and expensive when not: the rule is query-time,
// so correcting it changes no stored byte. Nothing has to be migrated. What
// cannot be recovered is the interval during which callers acted on empty
// answers believing them.
//
// The guard is therefore structural rather than hopeful. [Visible] is the ONLY
// place in this module where a validity bound and a transaction identifier are
// both compared, so a caller passing one value into both parameters is
// reviewable in one file, and a test asserts no package outside this one names
// both axes.
//
// # Intervals are half-open
//
// A validity interval is [From, To). Adjacent intervals therefore neither
// overlap nor leave a gap, and exactly one of them is visible at any instant.
// Closed intervals produce the off-by-one where a value appears twice at the
// boundary.
//
// The decision this package implements is recorded in
// docs/adr/ADR-002-transaction-identity.md, whose rule 6 states the defaults as
// a table. [ResolveQualifiers] is that table.
package temporal
