// Package mcpsurface decides what a machine caller may ask this engine, and
// what the asking means.
//
// The caller here is a model, and it is the least forgiving caller there is: it
// cannot open the documentation, cannot read a changelog, and cannot ask what an
// error meant. It has the tool list and nothing else. Everything in this package
// follows from that.
//
// # A tool is a query, or it is not a tool
//
// Every [Tool] compiles to a [github.com/atvirokodosprendimai/sdev1/internal/core/ql]
// statement. Nothing here reaches storage, and nothing here decides what a time
// qualifier defaults to.
//
// ⚠ The alternative — a handler per question, each fetching what it needs — is
// the shape this package exists to prevent. It is a second query language: one
// with no grammar, no written semantics, a different time story per tool, and
// its own bugs in every one. The two-axis defaults table, the rule that an
// optional leg yields an unbound value rather than dropping the row, the stated
// similarity metric and threshold — all of those are properties of the LANGUAGE.
// A handler that skips the language re-implements each of them and diverges on
// absent attributes and historical reads, which are not the cases anyone tests.
//
// # The tenant is not the caller's to choose
//
// [Session] carries the tenant; [Call] never does. An argument named
// [TenantArg] is IGNORED rather than rejected, because a rejection tells the
// caller the parameter exists.
//
// ⚠ This is not defensiveness about a hostile operator. A model's next tool call
// is a function of the text it just read, and some of that text came out of this
// store — so the read itself is the injection vector, and a tenant the model can
// name is a tenant it can be talked into naming. The argument does not exist,
// which is why there is nothing to bypass.
//
// # A refusal is a value, not an error
//
// [Refusal] deliberately has no Error method, so no transport can carry it as a
// protocol error.
//
// ⚠ A protocol error is what a dropped connection looks like, and the correct
// response to a dropped connection is to retry. An agent handed a refusal as an
// error therefore retries it forever, at its own expense and the cluster's. A
// refusal is a result that names the tool and says why, which is something a
// model can act on.
//
// # There is no update and no delete
//
// The store appends; retraction is an assertion and erasure is the destruction of
// a key. [Registry.Register] refuses a tool whose name uses a mutation verb.
//
// ⚠ The damage from an `update` tool is not at the API. It is that the model
// learns a data model this system does not have, and then reasons about history,
// retraction and erasure wrongly — a failure in the caller's reasoning, which
// nothing reports.
//
// The surface is read-only, and that is a CONSEQUENCE rather than a policy: the
// language has no write statement, so there is nothing for a write tool to
// compile to.
//
// # What this package does not do
//
// It serves nothing and speaks no protocol. [Registry.Compile] returns a
// statement and the tenant-scoped key that statement addresses, and stops.
// Keeping meaning separate from transport is what makes all of the above
// testable with no server, no SDK and no storage engine.
package mcpsurface
