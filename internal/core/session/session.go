package session

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/link"
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

	// store is the durable half, or nil for a session that holds everything in
	// memory. ⚠ A nil store is not a degraded mode: it is what ADR-022 built, and
	// every statement behaves identically either way.
	store *leafstore.Store
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

// Open returns a session backed by a leaf on a disk, rehydrated from what the
// leaf already holds.
//
// ⚠ It reads [leafstore.Store.History] rather than Load. No snapshot returns all
// of history — an instant on the business axis selects the facts true AT it — so
// a session rebuilt through a snapshot would silently drop everything that had
// stopped being true.
//
// ★ Rehydration also OBSERVES every identifier it loads. A restart is the same
// thing as receiving a timestamp from somewhere else, and the clock already knows
// how to handle that. Without it a session restarted against an earlier clock
// would mint identifiers that sort BEFORE the facts it just loaded, and a new
// assertion would quietly lose to an old one.
func Open(tenant addr.TenantID, now func() int64, store *leafstore.Store) (*Session, error) {
	s := New(tenant, now)
	s.store = store

	entities, err := store.Entities()
	if err != nil {
		return nil, fmt.Errorf("session: listing the leaf: %w", err)
	}
	for _, entity := range entities {
		history, err := store.History(entity)
		if err != nil {
			return nil, fmt.Errorf("session: rehydrating %q: %w", entity, err)
		}
		for _, d := range history {
			s.minter.Observe(d.TxID)
			if err := s.record(d); err != nil {
				return nil, fmt.Errorf("session: rehydrating %q: %w", entity, err)
			}
		}
	}
	return s, nil
}

// Seal writes everything written since the last seal into a segment.
//
// It is a no-op on a session with no store, so a caller need not ask which kind
// it holds.
func (s *Session) Seal(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.Seal(ctx)
}

// Close releases the store. It does NOT seal.
//
// ⚠ Deliberately: ADR-020 says an acknowledged write is held in memory, so an
// unsealed tail is lost on exit. Sealing here would make the commit point depend
// on how a process happened to end.
func (s *Session) Close() error {
	if s.store == nil {
		return nil
	}
	return s.store.Close()
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
	// Reached is what a TRAVERSE walked to, nearest the root first.
	Reached []link.Path
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
	case *ql.Traverse:
		return s.traverse(src, v)
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
		// Carried through from how it was WRITTEN, never re-derived from bytes.
		IsReference: w.ValueIsReference,
	}
	if err := s.record(datom); err != nil {
		return Result{}, err
	}

	// The durable half, when there is one. ⚠ It appends to a tail in memory and
	// touches no disk: ADR-020 fixed the commit point at replicas in memory, and
	// flushing here would move it as a side effect.
	if s.store != nil {
		if err := s.store.Append(context.Background(), datom); err != nil {
			return Result{}, fmt.Errorf("session: append to the store: %w", err)
		}
	}

	return Result{Statement: src, Wrote: &datom}, nil
}

// record puts a datom into everything this session answers from.
//
// ⚠ ONE path, used by a live write and by rehydration alike. A rehydration that
// restored the datom map and forgot the index is the quietest failure available
// here: SELECT works, the restart obviously worked, and SEARCH returns nothing
// with no error anywhere. Having one place to populate is what makes that
// unwritable rather than merely unlikely.
func (s *Session) record(d ports.Datom) error {
	s.datoms[d.Entity] = append(s.datoms[d.Entity], d)
	if !contains(s.written, d.Entity) {
		s.written = append(s.written, d.Entity)
	}

	// Index on the WRITE path, so a search finds facts that were asserted rather
	// than facts something indexed separately.
	//
	// ⚠ A REFERENCE is not indexed as text. Its bytes are an entity name, not
	// prose, and full-text matching them would answer "what links to this" with
	// something that only looks like an answer — inbound edges are a different
	// index and a deferred decision, not a side effect of the analyzer.
	if d.Assert && !d.IsReference {
		for _, term := range search.Analyze(string(d.Value)) {
			posting := search.Posting{Subject: d.Entity, Term: term, From: d.TxID}
			sealed, err := search.Seal(s.keys, posting)
			if err != nil {
				return fmt.Errorf("session: index the write: %w", err)
			}
			s.index.Add(term, sealed)
		}
	}
	return nil
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

// traverse walks the links out of an entity, at ONE instant.
//
// ⚠ The snapshot is resolved once, here, and `link.Walk` hands that same value to
// every hop. Resolving per hop would assemble a tree out of parts that each
// existed and that as a whole never did.
func (s *Session) traverse(src string, t *ql.Traverse) (Result, error) {
	at := t.Time.Resolve(s.now())

	reached, err := link.Walk(&datomLinks{session: s}, t.Root, at, t.Depth)
	if err != nil {
		return Result{}, err
	}
	return Result{Statement: src, Reached: reached}, nil
}

// datomLinks resolves an entity's outbound references from the session's datoms.
//
// ★ It exists so `link.Walk` is handed a resolver rather than reaching into
// storage itself — which is what keeps the same-instant rule testable and keeps
// the walk unaware of where datoms live.
type datomLinks struct{ session *Session }

func (d *datomLinks) References(entity string, at temporal.Query) ([]string, error) {
	// Latest visible datom per attribute, exactly as a SELECT would see it.
	latest := make(map[string]ports.Datom)
	for _, dat := range d.session.datoms[entity] {
		if !temporal.Visible(dat.Valid.From, dat.Valid.To, dat.TxID, at) {
			continue
		}
		if prior, seen := latest[dat.Attribute]; seen && dat.TxID.Compare(prior.TxID) <= 0 {
			continue
		}
		latest[dat.Attribute] = dat
	}

	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	// Sorted so a walk is reproducible; a map would order hops differently on
	// every run, which is the defect placement shipped.
	sort.Strings(names)

	var out []string
	for _, name := range names {
		dat := latest[name]
		// ⚠ A retraction suppresses the link, and only a REFERENCE is followed.
		// A literal whose bytes spell an entity name is not an edge.
		if !dat.Assert || !dat.IsReference {
			continue
		}
		out = append(out, string(dat.Value))
	}
	return out, nil
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
