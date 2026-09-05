package mcpsurface

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
)

// TimeArg is the argument every tool must accept.
//
// ★ Time is a CLAUSE in the language rather than a family of verbs, and the
// surface inherits that or re-grows the family: a read beside a read_history
// beside a read_as_of, each with its own idea of what the default is.
const TimeArg = "as_of"

// TenantArg is the argument name a call may carry and this package always
// ignores.
//
// ⚠ It is named so that ignoring it is deliberate and visible. Rejecting it
// would tell a caller the parameter exists.
const TenantArg = "tenant"

// EntityArg names the subject a tool reads.
const EntityArg = "entity"

// Verb is what a tool does. The set is CLOSED.
//
// ★ An open set means the thing on the other end pattern-matches strings, which
// is cheap at two verbs and unpayable at twenty.
type Verb int

const (
	// VerbUnset is the zero value and is never valid.
	VerbUnset Verb = iota
	// VerbRead compiles to a [ql.Read]. ★ The surface named it `read` before the
	// language did (ADR-013); ADR-034 made the two agree.
	VerbRead
	// VerbResemble compiles to a [ql.ShapeQuery].
	VerbResemble
)

func (v Verb) String() string {
	switch v {
	case VerbRead:
		return "read"
	case VerbResemble:
		return "resemble"
	default:
		return "unset"
	}
}

// Arg is one named parameter a tool takes.
type Arg struct {
	Name     string
	Required bool
	// Help is prose the agent reads. It is the only explanation it will get.
	Help string
}

// Tool is one thing an agent may call.
type Tool struct {
	Name    string
	Verb    Verb
	Summary string
	// Refusals states every way this tool says no.
	//
	// ★ Required and non-empty. A description that says what a tool does and not
	// what it refuses produces a caller that retries a refusal forever — the same
	// failure as [Refusal] being an error, one layer up.
	Refusals []string
	Args     []Arg
}

// Registration failures. These are startup defects rather than things an agent
// did, so they are ordinary Go errors and not a [Refusal].
var (
	// ErrNoName reports a tool with no name.
	ErrNoName = errors.New("mcpsurface: a tool needs a name")
	// ErrUnknownVerb reports a tool whose verb is outside the closed set.
	ErrUnknownVerb = errors.New("mcpsurface: a tool needs a declared verb")
	// ErrNoSummary reports a tool with nothing for an agent to read.
	ErrNoSummary = errors.New("mcpsurface: a tool needs a summary")
	// ErrNoRefusals reports a tool that does not say how it says no.
	ErrNoRefusals = errors.New("mcpsurface: a tool must declare its refusals")
	// ErrNoTimeArgument reports a tool that does not take a time argument.
	//
	// ★ This is what stops a get_history growing beside a get.
	ErrNoTimeArgument = errors.New("mcpsurface: a tool must take the " + TimeArg + " argument, because time is a clause and not a second tool")
	// ErrMutationName reports a tool named for a verb the store does not have.
	ErrMutationName = errors.New("mcpsurface: the store appends, so there is no update and no delete; name the tool for what it reads or asserts")
	// ErrDuplicateTool reports a name registered twice.
	ErrDuplicateTool = errors.New("mcpsurface: a tool of that name is already declared")
)

// mutationWords are the verb names this store does not have.
//
// ⚠ Matched against whole underscore-separated SEGMENTS, never as substrings.
// "set" is a substring of "asset", and a substring match would refuse
// read_asset — a rule that fires on the wrong thing gets switched off, and then
// it protects nothing.
var mutationWords = map[string]bool{
	"update": true, "delete": true, "set": true, "patch": true,
	"modify": true, "drop": true, "remove": true, "put": true,
}

// Session is what the caller does not choose.
//
// ⚠ The zero value names no tenant and is REFUSED. A default tenant is how every
// misconfigured agent quietly reads the same tenant's data, and the symptom is
// answers that look correct.
type Session struct {
	tenant addr.TenantID
	bound  bool
}

// NewSession binds a session to a tenant.
func NewSession(t addr.TenantID) Session { return Session{tenant: t, bound: true} }

// Tenant returns the session's tenant and whether one was bound.
func (s Session) Tenant() (addr.TenantID, bool) { return s.tenant, s.bound }

// Call is one invocation from an agent.
type Call struct {
	Tool string
	Args map[string]string
}

// Refusal is an answer the agent reads.
//
// ⚠ It has no Error method, deliberately, and [Registry.Compile] returns it
// beside the result rather than in an error position. A refusal a transport
// carries as a protocol error is indistinguishable from a dropped connection,
// and an agent retries a dropped connection.
type Refusal struct {
	// Tool is what refused.
	Tool string
	// Code is a stable token the caller can branch on.
	Code string
	// Reason is prose the caller can act on.
	Reason string
}

func (r *Refusal) String() string {
	return fmt.Sprintf("%s refused (%s): %s", r.Tool, r.Code, r.Reason)
}

// Compiled is what a call means.
type Compiled struct {
	// Statement is the query the call is equivalent to.
	Statement ql.Statement
	// Key is the tenant-scoped address the statement reads, built from the
	// SESSION's tenant.
	Key addr.Key
}

// Registry holds the declared tools.
type Registry struct {
	order []string
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register declares a tool, or says why it cannot be one.
func (r *Registry) Register(t Tool) error {
	if strings.TrimSpace(t.Name) == "" {
		return ErrNoName
	}
	if t.Verb != VerbRead && t.Verb != VerbResemble {
		return fmt.Errorf("%w: %q", ErrUnknownVerb, t.Name)
	}
	if strings.TrimSpace(t.Summary) == "" {
		return fmt.Errorf("%w: %q", ErrNoSummary, t.Name)
	}
	if len(t.Refusals) == 0 {
		return fmt.Errorf("%w: %q", ErrNoRefusals, t.Name)
	}
	for _, segment := range strings.Split(t.Name, "_") {
		if mutationWords[segment] {
			return fmt.Errorf("%w: %q", ErrMutationName, t.Name)
		}
	}
	if !takesTime(t) {
		return fmt.Errorf("%w: %q", ErrNoTimeArgument, t.Name)
	}
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateTool, t.Name)
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
	return nil
}

// takesTime reports whether a tool accepts the time argument.
func takesTime(t Tool) bool {
	for _, a := range t.Args {
		if a.Name == TimeArg {
			return true
		}
	}
	return false
}

// Tools returns the declared tools in declaration order.
//
// ★ This is the only source a server may publish from. A list copied once drifts
// the moment anything registers conditionally, and the drift is invisible: the
// agent simply never sees the tool.
func (r *Registry) Tools() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Lookup returns a declared tool by name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Compile turns a call into the query it means, or into a refusal the agent can
// read.
//
// ⚠ Exactly one of the two returns is meaningful, and the refusal is NOT an
// error. See [Refusal].
func (r *Registry) Compile(s Session, c Call) (Compiled, *Refusal) {
	t, ok := r.tools[c.Tool]
	if !ok {
		return Compiled{}, &Refusal{
			Tool:   c.Tool,
			Code:   "unknown-tool",
			Reason: fmt.Sprintf("no tool named %q is declared; call tools/list to see what is", c.Tool),
		}
	}
	// ⚠ The tenant comes from the SESSION. c.Args[TenantArg] is read by nothing
	// in this function, and adding a read of it is the change this package's
	// falsifier exists to catch.
	tenant, bound := s.Tenant()
	if !bound {
		return Compiled{}, &Refusal{
			Tool:   t.Name,
			Code:   "unbound-session",
			Reason: fmt.Sprintf("tool %q needs a session bound to a tenant, and there is no default", t.Name),
		}
	}
	for _, a := range t.Args {
		if a.Required && strings.TrimSpace(c.Args[a.Name]) == "" {
			return Compiled{}, &Refusal{
				Tool:   t.Name,
				Code:   "missing-argument",
				Reason: fmt.Sprintf("tool %q requires the argument %q: %s", t.Name, a.Name, a.Help),
			}
		}
	}
	when, refusal := timeClause(t.Name, c.Args[TimeArg])
	if refusal != nil {
		return Compiled{}, refusal
	}
	entity := strings.TrimSpace(c.Args[EntityArg])
	key := addr.KeyOf(tenant, entity)

	switch t.Verb {
	case VerbRead:
		return Compiled{
			Statement: &ql.Read{
				Entity:     entity,
				Attributes: splitList(c.Args["attributes"]),
				Time:       when,
			},
			Key: key,
		}, nil
	case VerbResemble:
		threshold, err := strconv.ParseFloat(strings.TrimSpace(c.Args["threshold"]), 64)
		if err != nil {
			return Compiled{}, &Refusal{
				Tool:   t.Name,
				Code:   "bad-threshold",
				Reason: fmt.Sprintf("tool %q needs a numeric threshold, and %q is not one", t.Name, c.Args["threshold"]),
			}
		}
		return Compiled{
			Statement: &ql.ShapeQuery{
				Subject:   entity,
				Legs:      legs(c.Args["require"], c.Args["optional"]),
				Metric:    strings.TrimSpace(c.Args["metric"]),
				Threshold: threshold,
				Time:      when,
			},
			Key: key,
		}, nil
	}
	// Unreachable while Register refuses an unknown verb. It is a refusal rather
	// than a panic so that a verb added without a compile arm produces a nil
	// statement the falsifier catches, rather than crashing a server.
	return Compiled{}, &Refusal{
		Tool:   t.Name,
		Code:   "no-compilation",
		Reason: fmt.Sprintf("tool %q declares verb %s, which compiles to no statement", t.Name, t.Verb),
	}
}

// timeClause reads the time argument as WRITTEN, leaving defaults to
// [ql.TimeClause.Resolve].
//
// ★ Resolving here would be the second implementation of ADR-002's defaults
// table, and two drift invisibly until a query returns the wrong history.
func timeClause(tool, raw string) (ql.TimeClause, *Refusal) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ql.TimeClause{}, nil
	}
	at, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return ql.TimeClause{}, &Refusal{
			Tool:   tool,
			Code:   "bad-time",
			Reason: fmt.Sprintf("tool %q reads %s as an instant, and %q is not one", tool, TimeArg, raw),
		}
	}
	return ql.TimeClause{ValidAt: &at}, nil
}

// splitList reads a comma-separated argument, dropping empties.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// legs builds a shape query's legs, required ones first.
func legs(required, optional string) []ql.Leg {
	var out []ql.Leg
	for _, a := range splitList(required) {
		out = append(out, ql.Leg{Attribute: a, Kind: ql.LegRequired})
	}
	for _, a := range splitList(optional) {
		out = append(out, ql.Leg{Attribute: a, Kind: ql.LegOptional})
	}
	return out
}

// Describe renders the text an agent reads.
//
// ★ Refusals are part of it, not an appendix. This is the only documentation
// this caller will ever have.
func Describe(t Tool) string {
	var b strings.Builder
	b.WriteString(t.Summary)
	b.WriteString("\n\nArguments:\n")
	for _, a := range t.Args {
		need := "optional"
		if a.Required {
			need = "required"
		}
		fmt.Fprintf(&b, "  %s (%s) — %s\n", a.Name, need, a.Help)
	}
	b.WriteString("\nRefuses:\n")
	for _, r := range t.Refusals {
		fmt.Fprintf(&b, "  - %s\n", r)
	}
	return b.String()
}
