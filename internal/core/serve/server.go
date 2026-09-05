package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/eval"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

var (
	// ErrWriteNotServed reports a write statement offered over the wire.
	//
	// ⚠ It travels as a [wire.Refusal] and NEVER as an empty answer. A client
	// reading zero rows would conclude the write landed and matched nothing —
	// which is why ADR-043 made "I will not" a distinct outcome from "not here"
	// and from "here it is".
	//
	// The refusal is not caution. There is no leader, so a write served here
	// would be unfenced (ADR-009) and committed at a durability nobody has
	// (ADR-020).
	ErrWriteNotServed = errors.New("serve: this node does not serve writes")

	// ErrNoTimeout reports a server built without a deadline or a frame bound.
	//
	// ⚠ There is no default, and the omission is the point: a connection with no
	// deadline is a goroutine a stranger can pin forever, and "not configured
	// yet" is indistinguishable from "deliberately unbounded" once a default
	// papers over it.
	ErrNoTimeout = errors.New("serve: read and write timeouts and a frame bound must all be positive")

	// ErrNoLeaf reports a server that was not told which leaf it holds.
	//
	// ★ Depth 0 is the root, which is every key. A node claiming it would serve
	// every request from one leaf's store and NEVER redirect — the failure is
	// silent, and it looks exactly like a correctly placed node until the second
	// node appears.
	ErrNoLeaf = errors.New("serve: a node must say which leaf it holds")
)

// Options is what a server needs, and it has no usable zero value.
type Options struct {
	// Addr is the address to listen on. `127.0.0.1:0` picks a free port.
	Addr string

	// Leaf is the leaf this node holds. Its depth is the depth every incoming
	// key is descended to.
	Leaf addr.LeafID

	// Store answers reads for that leaf.
	//
	// ★ A [ports.Reader], not a *leafstore.Store: the server does not care where
	// the datoms live, and the narrower type is what lets a test drive it.
	Store ports.Reader

	// Table is this node's belief about where every prefix lives, and the ONLY
	// routing input. ⚠ A request's key is looked up here; what the caller
	// believed is never consulted.
	Table *routing.Table

	// ReadTimeout bounds reading one request; WriteTimeout bounds writing one
	// response. Both are set on the connection, never on the listener.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// MaxFrame bounds a frame in either direction. See [wire.MaxFrame] for a
	// value an operator may adopt.
	MaxFrame int
}

// Server answers one request per connection.
type Server struct {
	opts     Options
	listener net.Listener

	// wg tracks connection goroutines so Close does not return while one is
	// still writing.
	wg sync.WaitGroup

	// closing is closed by Close, and is what tells the accept loop that a
	// failed Accept is an ordinary shutdown rather than a fault.
	closing chan struct{}
	once    sync.Once
}

// NewServer validates the options and binds the listener.
//
// ⚠ It refuses rather than defaults. Every check here is for something whose
// absence is invisible at run time: an unbounded connection looks like a fast
// one, and a root leaf looks like a correctly placed node until a second node
// exists to be redirected to.
func NewServer(opts Options) (*Server, error) {
	if opts.ReadTimeout <= 0 || opts.WriteTimeout <= 0 || opts.MaxFrame <= 0 {
		return nil, fmt.Errorf("%w: read %v, write %v, frame %d",
			ErrNoTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.MaxFrame)
	}
	if opts.Leaf.Depth == 0 {
		return nil, ErrNoLeaf
	}
	if opts.Store == nil {
		return nil, errors.New("serve: a node must be given a store to read")
	}
	if opts.Table == nil {
		return nil, errors.New("serve: a node must be given a routing table")
	}

	l, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("serve: listening on %q: %w", opts.Addr, err)
	}
	return &Server{opts: opts, listener: l, closing: make(chan struct{})}, nil
}

// Addr is where the server is actually listening, which is what a test needs
// after asking for port 0.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Serve accepts until [Server.Close].
//
// It returns nil on an ordinary shutdown. ⚠ An accept failure after Close is not
// an error: reporting one would make every clean exit look like a fault.
func (s *Server) Serve(ctx context.Context) error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closing:
				return nil
			default:
				return fmt.Errorf("serve: accepting: %w", err)
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

// Close stops accepting and waits for connections in flight.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closing)
		err = s.listener.Close()
	})
	s.wg.Wait()
	return err
}

// handle runs exactly one exchange and closes the connection.
//
// ⚠ The deadlines go on the CONNECTION. A listener deadline bounds Accept, and
// the goroutine a stranger can pin forever is this one.
func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if err := conn.SetReadDeadline(time.Now().Add(s.opts.ReadTimeout)); err != nil {
		return
	}
	payload, err := wire.ReadFrame(conn, s.opts.MaxFrame)
	if err != nil {
		// ⚠ Nothing is sent back. A frame that could not be read may not have
		// come from something that speaks this protocol at all, and a reply
		// would be a reflection an unbounded number of strangers can aim.
		return
	}
	req, err := wire.DecodeRequest(payload)
	if err != nil {
		s.reply(conn, &wire.Refusal{Reason: fmt.Sprintf("serve: %v", err)})
		return
	}
	s.reply(conn, s.answer(ctx, req))
}

// reply writes one response under the write deadline.
func (s *Server) reply(conn net.Conn, resp wire.Response) {
	if err := conn.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout)); err != nil {
		return
	}
	body, err := wire.Encode(resp)
	if err != nil {
		return
	}
	_ = wire.WriteFrame(conn, body, s.opts.MaxFrame)
}

// answer decides what one request gets, and is the whole of ADR-045 rule 2.
//
// ★★ THE KEY IS DESCENDED AGAINST THIS NODE'S OWN LEAF. The caller's belief about
// placement is not an input and is not even representable in a request — which is
// exactly why a node that does not hold the key can still say where it went.
func (s *Server) answer(ctx context.Context, req wire.Request) wire.Response {
	leaf, err := addr.Descend(req.Key, s.opts.Leaf.Depth)
	if err != nil {
		return &wire.Refusal{Reason: fmt.Sprintf("serve: descending the key: %v", err)}
	}
	if leaf != s.opts.Leaf {
		return s.redirect(req.Key, leaf)
	}
	return s.read(ctx, req)
}

// redirect says where the key went, according to THIS node's table.
//
// ⚠ A route with no entry is a refusal, not a zero route. `routing.ErrNoRoute`
// exists because a zero route is a next hop of nowhere, and a client handed one
// would send a request into the dark rather than report that nobody knows.
func (s *Server) redirect(k addr.Key, leaf addr.LeafID) wire.Response {
	route, err := s.opts.Table.Lookup(k)
	if err != nil {
		return &wire.Refusal{Reason: fmt.Sprintf("serve: %s is not this node's leaf and no route covers it: %v", leaf, err)}
	}
	return &wire.Redirect{Route: route}
}

// read parses a statement and serves it, or refuses it by name.
func (s *Server) read(ctx context.Context, req wire.Request) wire.Response {
	stmt, err := ql.Parse(req.Statement)
	if err != nil {
		return &wire.Refusal{Reason: fmt.Sprintf("serve: %v", err)}
	}

	sel, ok := stmt.(*ql.Read)
	if !ok {
		// ⚠ By name, and as a REFUSAL. An empty answer here is the shape that
		// tells a client its write succeeded and matched nothing.
		return &wire.Refusal{Reason: fmt.Sprintf("%v: %T", ErrWriteNotServed, stmt)}
	}

	rows, err := eval.Read(ctx, s.opts.Store, sel, req.Now)
	if err != nil {
		return &wire.Refusal{Reason: fmt.Sprintf("serve: %v", err)}
	}

	run, err := datom.Encode(datomsOf(rows))
	if err != nil {
		return &wire.Refusal{Reason: fmt.Sprintf("serve: encoding %d row(s): %v", len(rows), err)}
	}
	return &wire.Answer{Datoms: run}
}

// datomsOf renders evaluated rows as the datom run an answer carries.
//
// ★ `Assert` is true for every row and is not carried on a [eval.Row]: a
// projection holds what an entity CURRENTLY carries, and a retracted attribute is
// absent from it rather than present-and-false. Everything else — both temporal
// endpoints and the reference flag — travels because the row carries it, which is
// what a row had to start doing for this to be encodable without inventing
// anything.
func datomsOf(rows []eval.Row) []ports.Datom {
	out := make([]ports.Datom, 0, len(rows))
	for _, r := range rows {
		out = append(out, ports.Datom{
			Entity:      r.Entity,
			Attribute:   r.Attribute,
			Value:       r.Value,
			Valid:       r.Valid,
			TxID:        r.TxID,
			Assert:      true,
			IsReference: r.IsReference,
		})
	}
	return out
}
