package temporal

import "github.com/atvirokodosprendimai/sdev1/internal/core/tx"

// This file is a deliberately naive, slow reference implementation of
// visibility, transcribed from ADR-002's rules rather than from Visible's code.
//
// ⚠ IT MUST NOT CALL OR COPY Visible. A test that compares an implementation
// against itself proves the code equals itself and nothing else. The value here
// comes entirely from the two having been written independently, which is a
// review obligation rather than something a gate can check — so a reviewer's
// job on this file is to confirm it reads like the RECORD, not like the
// implementation.
//
// It is split into two helpers on purpose. ADR-002 states two conditions and
// says they are independent; writing them as one expression would quietly
// encode a relationship between them that the record does not claim.

// oracleBusinessVisible transcribes: "the datom's business interval must
// contain the query's ValidAt", where the interval is half-open [From, To) —
// every instant from From onward, up to but excluding To.
func oracleBusinessVisible(validFrom, validTo, validAt int64) bool {
	if validAt < validFrom {
		return false
	}
	if validAt >= validTo {
		return false
	}
	return true
}

// oracleTransactionVisible transcribes: "the datom's TxID must not exceed the
// query's AsOf", and "an open AsOf never excludes anything".
func oracleTransactionVisible(id tx.TxID, asOf *tx.TxID) bool {
	if asOf == nil {
		return true
	}
	return id.Compare(*asOf) <= 0
}

// oracleVisible combines the two independent conditions.
func oracleVisible(validFrom, validTo int64, id tx.TxID, q Query) bool {
	business := true
	if q.ValidAt != nil {
		business = oracleBusinessVisible(validFrom, validTo, *q.ValidAt)
	}
	return business && oracleTransactionVisible(id, q.AsOf)
}
