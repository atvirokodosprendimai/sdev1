package ports

import (
	"context"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Datom is one fact: an entity had an attribute with a value, over a business
// interval, recorded by a transaction.
//
// Assert distinguishes a fact from its withdrawal. A retraction is a datom with
// Assert cleared, NEVER an absent datom — "this stopped being true" and "this
// was never recorded" are different facts, and only the first can be stated by
// an absence in a store that overwrites.
type Datom struct {
	Entity    string
	Attribute string
	Value     []byte
	Valid     temporal.Interval
	TxID      tx.TxID
	Assert    bool

	// IsReference says the value names another ENTITY rather than being data.
	//
	// ⚠ Stored, never inferred. "planet-9" as a name and "planet-9" as a link
	// are the same nine bytes; only this field tells them apart. Guessing from
	// the shape of the value would make every identifier-looking string an
	// accidental edge, and the graph would change whenever unrelated data did.
	//
	// It is a field on the datom rather than a separate edge record for the same
	// reason `Assert` is: a link is not a new kind of thing, so it inherits
	// bitemporality, retraction and the transaction boundary without any of them
	// being decided again. The standalone typed form is
	// [github.com/atvirokodosprendimai/sdev1/internal/core/link.Value].
	IsReference bool
}

// Snapshot is the pair of values a read is evaluated at: a transaction
// identifier bounding the system axis, and an instant on the business axis.
//
// It is deliberately not a new mechanism. These are exactly the two qualifiers
// the visibility predicate already takes, carried together so a caller cannot
// supply one and forget the other.
type Snapshot struct {
	At      tx.TxID
	ValidAt int64
}

// Query renders the snapshot as the bound query the visibility predicate takes.
//
// ⚠ The conversion lives here, and the assembly itself lives in
// [github.com/atvirokodosprendimai/sdev1/internal/core/temporal.At], so that no
// store has to name both axes in its own file. A store that built the query
// itself would be a second place where one instant can be passed into two
// parameters — the defect this pair of types exists to make unwritable.
func (s Snapshot) Query() temporal.Query {
	return temporal.At(s.At, s.ValidAt)
}

// Reader loads datoms. It has no method that mutates, and that absence is the
// contract: a read model is handed a Reader and therefore cannot write.
//
// ⚠ Adding a mutating method here silently widens what EVERY read model may do.
// A test enumerates this interface's method set for that reason.
type Reader interface {
	// Load returns the datoms of one entity visible at a snapshot.
	Load(ctx context.Context, entity string, at Snapshot) ([]Datom, error)

	// Attributes returns the attribute names an entity carries at a snapshot,
	// which is the entity's shape and the basis of a similarity query.
	Attributes(ctx context.Context, entity string, at Snapshot) ([]string, error)
}

// Writer appends datoms. It has no method that reads.
//
// The write path is handed [Store] rather than a Writer alone, because a writer
// answers its own validation questions — but it answers them from its own
// state, which is what Store's Reader half provides. It is never handed a read
// model.
type Writer interface {
	// Append records datoms durably. All of them belong to one entity; the
	// transaction type enforces that before anything reaches here.
	Append(ctx context.Context, datoms ...Datom) error
}

// Store is both halves, and is what the write path is handed.
type Store interface {
	Reader
	Writer
}

// Publisher announces that a leaf changed.
//
// It carries an identifier and never state. A consumer re-reads for itself, so
// two notifications racing for one leaf both re-read and the later answer wins.
// Publishing rendered state would allow an older render to arrive last and be
// applied over a newer one.
type Publisher interface {
	Publish(ctx context.Context, leaf addr.LeafID, at tx.TxID) error
}
