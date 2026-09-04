package ql_test

import (
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
)

// Example_parseAndResolve is the snippet printed in docs/QUERY-LANGUAGE.md.
//
// ★ It lives here as a runnable example so the documentation cannot drift from
// the API without a test failing. A code block in a document that nothing
// executes is a claim, not a sample.
func Example_parseAndResolve() {
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

	// Output:
	// entity:     planet-7
	// attributes: [mass]
	// valid at:   1700000000
	// as of txn:  <nil>
	// resolved:   1700000000 <nil>
}
