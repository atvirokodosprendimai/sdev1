package session

import (
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/search"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrUnsupported reports a statement this session cannot run.
//
// ⚠ It is a named refusal rather than an empty result. "Nothing matched" and
// "this is not implemented" are different answers, and returning the first for
// the second is a lie a caller cannot see through.
var ErrUnsupported = errors.New("session: this session cannot run that statement")

// Session is an in-memory store that runs statements.
//
// ⚠ Not a storage engine. See the package comment.
type Session struct {
	tenant addr.TenantID
	minter *tx.Minter
	// now supplies a read's business instant.
	//
	// ⚠ A read must NOT mint a transaction to find out what time it is. Minting
	// advances the sequence, so reads would consume identifiers and the gaps
	// would look like lost writes to anyone reading a transcript.
	now     func() int64
	keys    *crypt.MemoryKeystore
	index   *search.Index
	datoms  map[string][]ports.Datom
	written []string
}

// New returns a session reading the wall from now.
//
// The clock is injectable so a test can make transaction identifiers
// predictable; nothing else about the session varies.
func New(tenant addr.TenantID, now func() int64) *Session {
	return &Session{
		tenant: tenant,
		minter: tx.NewMinter(addr.LeafID{Depth: 1}, hlc.NewClock(now)),
		now:    now,
		keys:   crypt.NewMemoryKeystore(),
		index:  search.NewIndex(),
		datoms: make(map[string][]ports.Datom),
	}
}

// Result is what one statement did.
type Result struct {
	// Statement is what was run, echoed for a transcript.
	Statement string
	// Wrote is the datom a write produced, or nil.
	Wrote *ports.Datom
	// Rows are the attribute/value pairs a SELECT returned.
	Rows []Row
	// Hits are the subjects a SEARCH returned, best first.
	Hits []search.Scored
	// Facets are the breakdowns a SEARCH asked for.
	Facets []search.FacetResult
}

// Row is one visible attribute of one entity.
type Row struct {
	Entity    string
	Attribute string
	Value     string
	// TxID is the transaction that recorded it — the axis a caller cannot set.
	TxID tx.TxID
}

// Run parses and applies one statement.
func (s *Session) Run(src string) (Result, error) {
	stmt, err := ql.Parse(src)
	if err != nil {
		return Result{}, err
	}
	switch v := stmt.(type) {
	case *ql.Write:
		return s.write(src, v)
	case *ql.Select:
		return s.selectFrom(src, v)
	case *ql.Search:
		return s.search(src, v)
	default:
		return Result{}, fmt.Errorf("%w: %T needs a similarity metric chosen against a corpus", ErrUnsupported, stmt)
	}
}

// write applies an ASSERT or a RETRACT.
//
// ⚠ The transaction is minted HERE. The language makes transaction time
// unsayable, and this is the layer that must not quietly reintroduce it — so
// nothing from the statement reaches the identifier.
func (s *Session) write(src string, w *ql.Write) (Result, error) {
	id := s.minter.Mint()

	datom := ports.Datom{
		Entity:    w.Entity,
		Attribute: w.Attribute,
		Value:     []byte(w.Value),
		Valid:     w.Interval(id.HLC.Wall),
		TxID:      id,
		Assert:    w.Op == ql.OpAssert,
	}
	s.datoms[w.Entity] = append(s.datoms[w.Entity], datom)
	if !contains(s.written, w.Entity) {
		s.written = append(s.written, w.Entity)
	}

	// Index on the WRITE path, so a search finds facts that were asserted rather
	// than facts something indexed separately.
	if datom.Assert {
		for _, term := range search.Analyze(w.Value) {
			posting := search.Posting{Subject: w.Entity, Term: term, From: id}
			sealed, err := search.Seal(s.keys, posting)
			if err != nil {
				return Result{}, fmt.Errorf("session: index the write: %w", err)
			}
			s.index.Add(term, sealed)
		}
	}

	return Result{Statement: src, Wrote: &datom}, nil
}

// selectFrom answers a SELECT at the resolved snapshot.
func (s *Session) selectFrom(src string, sel *ql.Select) (Result, error) {
	resolved := sel.Time.Resolve(s.now())

	wanted := make(map[string]bool, len(sel.Attributes))
	for _, a := range sel.Attributes {
		wanted[a] = true
	}

	// Latest visible datom per attribute, by transaction order.
	latest := make(map[string]ports.Datom)
	for _, d := range s.datoms[sel.Entity] {
		if len(wanted) > 0 && !wanted[d.Attribute] {
			continue
		}
		if !temporal.Visible(d.Valid.From, d.Valid.To, d.TxID, resolved) {
			continue
		}
		if prior, seen := latest[d.Attribute]; seen && d.TxID.Compare(prior.TxID) <= 0 {
			continue
		}
		latest[d.Attribute] = d
	}

	result := Result{Statement: src}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	// Sorted so two runs agree; a map would order this differently each time.
	sort.Strings(names)
	for _, name := range names {
		d := latest[name]
		// ⚠ A retraction is a datom, not an absence — so it SUPPRESSES the
		// attribute rather than being reported as a value.
		if !d.Assert {
			continue
		}
		result.Rows = append(result.Rows, Row{
			Entity:    d.Entity,
			Attribute: d.Attribute,
			Value:     string(d.Value),
			TxID:      d.TxID,
		})
	}
	return result, nil
}

// search answers a SEARCH against the index fed on the write path.
func (s *Session) search(src string, q *ql.Search) (Result, error) {
	values := make(map[string]map[string]string, len(q.Facets))
	for _, attribute := range q.Facets {
		values[attribute] = s.valuesOf(attribute)
	}

	out, err := s.index.Search(s.keys, search.Query{
		Text:       q.Query,
		Limit:      q.Limit,
		Facets:     q.Facets,
		FacetBound: q.Limit,
		Values:     values,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Statement: src, Hits: out.Hits, Facets: out.Facets}, nil
}

// valuesOf collects the current value of one attribute per entity, for faceting.
func (s *Session) valuesOf(attribute string) map[string]string {
	out := make(map[string]string)
	for entity, datoms := range s.datoms {
		var best *ports.Datom
		for i := range datoms {
			d := datoms[i]
			if d.Attribute != attribute {
				continue
			}
			if best == nil || d.TxID.Compare(best.TxID) > 0 {
				best = &datoms[i]
			}
		}
		if best != nil && best.Assert {
			out[entity] = string(best.Value)
		}
	}
	return out
}

// Entities returns every entity this session has written to, in insertion order.
func (s *Session) Entities() []string { return append([]string(nil), s.written...) }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
