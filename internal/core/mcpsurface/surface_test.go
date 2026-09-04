package mcpsurface

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
)

// theTenant is the session's tenant throughout. It is deliberately not zero, so
// a test cannot pass because an implementation defaulted.
var theTenant = addr.TenantFromUint(7)

// sampleArgs fills every argument a tool declares with a well-formed value.
//
// ⚠ It fails on an argument it does not know rather than skipping it. A helper
// that silently omitted an unknown argument would make a required-argument
// refusal look like a compile failure, and the test would report the wrong
// mechanism.
func sampleArgs(t *testing.T, tool Tool) map[string]string {
	t.Helper()
	args := make(map[string]string, len(tool.Args))
	for _, a := range tool.Args {
		switch a.Name {
		case EntityArg:
			args[a.Name] = "planet-7"
		case "attributes":
			args[a.Name] = "name,mass"
		case "require":
			args[a.Name] = "name"
		case "optional":
			args[a.Name] = "nickname"
		case "metric":
			args[a.Name] = "jaccard"
		case "threshold":
			args[a.Name] = "0.8"
		case TimeArg:
			args[a.Name] = "1700000000"
		default:
			t.Fatalf("tool %q declares argument %q that this test does not know how to fill; add it here rather than letting the call go out incomplete", tool.Name, a.Name)
		}
	}
	return args
}

// timeOf reads the time clause off whichever statement kind it was given.
func timeOf(t *testing.T, s ql.Statement) ql.TimeClause {
	t.Helper()
	switch v := s.(type) {
	case *ql.Select:
		return v.Time
	case *ql.ShapeQuery:
		return v.Time
	default:
		t.Fatalf("statement %T carries no time clause, so time is not a clause on every tool", s)
		return ql.TimeClause{}
	}
}

// wellFormedTool is a tool that satisfies every registration rule, so a test can
// break exactly one thing about it.
func wellFormedTool(name string) Tool {
	return Tool{
		Name:     name,
		Verb:     VerbRead,
		Summary:  "read something",
		Refusals: []string{"a session with no tenant"},
		Args: []Arg{
			{Name: EntityArg, Required: true, Help: "the entity"},
			{Name: TimeArg, Required: false, Help: "the instant"},
		},
	}
}

// TestEveryToolCompilesToAQuery is ADR-013's falsifier.
//
// ⚠ It walks Tools() — the same source a server publishes from — rather than a
// list written here. A test against its own list passes while the registry holds
// something else, which is exactly the drift it is meant to catch.
func TestEveryToolCompilesToAQuery(t *testing.T) {
	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}
	tools := r.Tools()
	if len(tools) == 0 {
		t.Fatal("the registry declares no tools, so this test proves nothing about a surface that offers an agent nothing")
	}
	session := NewSession(theTenant)

	for _, tool := range tools {
		compiled, refusal := r.Compile(session, Call{Tool: tool.Name, Args: sampleArgs(t, tool)})
		if refusal != nil {
			t.Fatalf("tool %q refused a well-formed call: %s", tool.Name, refusal.Reason)
		}
		if compiled.Statement == nil {
			t.Fatalf("tool %q compiled to no statement, so it answers by reaching past the language — which is the second query surface ADR-013 exists to prevent", tool.Name)
		}
		switch tool.Verb {
		case VerbRead:
			if _, ok := compiled.Statement.(*ql.Select); !ok {
				t.Fatalf("tool %q declares verb %s but compiled to %T", tool.Name, tool.Verb, compiled.Statement)
			}
		case VerbResemble:
			if _, ok := compiled.Statement.(*ql.ShapeQuery); !ok {
				t.Fatalf("tool %q declares verb %s but compiled to %T", tool.Name, tool.Verb, compiled.Statement)
			}
		default:
			t.Fatalf("tool %q declares verb %s, which is outside the closed set", tool.Name, tool.Verb)
		}

		// The time argument is carried AS WRITTEN. Resolving it here would be a
		// second implementation of ADR-002's defaults table, and two drift
		// invisibly until a query returns the wrong history.
		if at := timeOf(t, compiled.Statement).ValidAt; at == nil || *at != 1700000000 {
			t.Fatalf("tool %q dropped the %s argument on the way to a statement", tool.Name, TimeArg)
		}
		bare := sampleArgs(t, tool)
		delete(bare, TimeArg)
		compiled, refusal = r.Compile(session, Call{Tool: tool.Name, Args: bare})
		if refusal != nil {
			t.Fatalf("tool %q refused a call with no %s, which must mean now: %s", tool.Name, TimeArg, refusal.Reason)
		}
		if at := timeOf(t, compiled.Statement).ValidAt; at != nil {
			t.Fatalf("tool %q resolved a default instant itself; the clause must reach the language unresolved", tool.Name)
		}
	}
}

// TestTenantComesFromTheSessionNotTheCall passes a HOSTILE tenant argument.
//
// ⚠ A test that never supplied one would pass against an implementation that
// read it. The argument here names a different tenant than the session, which is
// the only shape that tells the two apart.
func TestTenantComesFromTheSessionNotTheCall(t *testing.T) {
	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}
	other := addr.TenantFromUint(999)
	if other == theTenant {
		t.Fatal("the hostile tenant equals the session's, so this test could not distinguish them")
	}

	for _, tool := range r.Tools() {
		args := sampleArgs(t, tool)
		args[TenantArg] = other.String()

		compiled, refusal := r.Compile(NewSession(theTenant), Call{Tool: tool.Name, Args: args})
		if refusal != nil {
			t.Fatalf("tool %q refused a call carrying a %s argument; it must be IGNORED, because a rejection tells the caller the parameter exists: %s", tool.Name, TenantArg, refusal.Reason)
		}
		if got := addr.TenantOf(compiled.Key); got != theTenant {
			t.Fatalf("tool %q compiled to a key belonging to tenant %s while the session is bound to %s — the call chose the tenant, and a model composes its next call from text it may have read out of this store", tool.Name, got, theTenant)
		}
		if want := addr.KeyOf(theTenant, "planet-7"); compiled.Key != want {
			t.Fatalf("tool %q addressed %v, want %v", tool.Name, compiled.Key, want)
		}
	}
}

// TestAnUnboundSessionIsRefusedNotDefaulted checks that nothing quietly picks a
// tenant.
func TestAnUnboundSessionIsRefusedNotDefaulted(t *testing.T) {
	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}
	tool := r.Tools()[0]

	compiled, refusal := r.Compile(Session{}, Call{Tool: tool.Name, Args: sampleArgs(t, tool)})
	if refusal == nil {
		t.Fatal("an unbound session compiled successfully, so it addressed some default tenant — which is how every misconfigured agent reads the same tenant's data, with answers that look correct")
	}
	if refusal.Code != "unbound-session" {
		t.Fatalf("refusal code is %q, want %q", refusal.Code, "unbound-session")
	}
	if compiled.Key != (addr.Key{}) {
		t.Fatal("a refused call still produced an address")
	}
	if compiled.Statement != nil {
		t.Fatal("a refused call still produced a statement")
	}
}

// TestARefusalIsNotAnError checks the property no signature can show.
//
// ⚠ A later Error method would make Refusal satisfy error without changing a
// single call site, so this is asserted at runtime rather than read off the type.
func TestARefusalIsNotAnError(t *testing.T) {
	var refusal any = &Refusal{Tool: "read_entity", Code: "unknown-tool", Reason: "no such tool"}
	if _, ok := refusal.(error); ok {
		t.Fatal("Refusal satisfies error, so a transport will carry a refusal in an error position — indistinguishable from a dropped connection, and the correct response to a dropped connection is to retry, so an agent retries the refusal forever")
	}
	var value any = Refusal{Tool: "read_entity", Code: "unknown-tool", Reason: "no such tool"}
	if _, ok := value.(error); ok {
		t.Fatal("Refusal satisfies error by value")
	}
}

// TestARefusalNamesTheToolAndTheReason checks a refusal is actionable rather
// than generic.
func TestARefusalNamesTheToolAndTheReason(t *testing.T) {
	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}
	session := NewSession(theTenant)

	cases := []struct {
		name string
		call Call
		code string
		says string
	}{
		{
			name: "unknown tool",
			call: Call{Tool: "no_such_tool"},
			code: "unknown-tool",
			says: "no_such_tool",
		},
		{
			name: "missing required argument",
			call: Call{Tool: "read_entity", Args: map[string]string{TimeArg: "1"}},
			code: "missing-argument",
			says: EntityArg,
		},
		{
			name: "unparseable instant",
			call: Call{Tool: "read_entity", Args: map[string]string{EntityArg: "planet-7", TimeArg: "yesterday"}},
			code: "bad-time",
			says: "yesterday",
		},
		{
			name: "unparseable threshold",
			call: Call{Tool: "find_resembling", Args: map[string]string{EntityArg: "planet-7", "metric": "jaccard", "threshold": "quite similar"}},
			code: "bad-threshold",
			says: "quite similar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compiled, refusal := r.Compile(session, tc.call)
			if refusal == nil {
				t.Fatalf("%s compiled instead of refusing", tc.name)
			}
			if compiled.Statement != nil {
				t.Fatal("a refused call still produced a statement")
			}
			if refusal.Code != tc.code {
				t.Fatalf("refusal code is %q, want %q", refusal.Code, tc.code)
			}
			if refusal.Tool != tc.call.Tool {
				t.Fatalf("refusal names tool %q, want %q", refusal.Tool, tc.call.Tool)
			}
			// ⚠ Asserting only "a refusal happened" would pass for a generic
			// message, and a generic message is what leaves an agent with
			// nothing to change before it tries again.
			if !strings.Contains(refusal.Reason, tc.says) {
				t.Fatalf("refusal reason %q does not say %q, so the caller learns nothing it can act on", refusal.Reason, tc.says)
			}
		})
	}
}

// TestAToolWithoutATimeArgumentIsRefused is what stops a get_history growing
// beside a get.
func TestAToolWithoutATimeArgumentIsRefused(t *testing.T) {
	r := NewRegistry()

	timeless := wellFormedTool("read_history")
	timeless.Args = []Arg{{Name: EntityArg, Required: true, Help: "the entity"}}
	if err := r.Register(timeless); !errors.Is(err, ErrNoTimeArgument) {
		t.Fatalf("registering a tool with no %s argument returned %v, want ErrNoTimeArgument — without this rule the temporal verb family ADR-011 rejected re-grows one tool at a time", TimeArg, err)
	}

	// Positive control: the same tool WITH a time argument registers, so the
	// refusal above is about the missing argument and not about the tool.
	if err := r.Register(wellFormedTool("read_history")); err != nil {
		t.Fatalf("the same tool with a %s argument was refused: %v", TimeArg, err)
	}
}

// TestMutationNamesAreRefusedAtRegistration checks the store's vocabulary is the
// surface's vocabulary.
func TestMutationNamesAreRefusedAtRegistration(t *testing.T) {
	for _, name := range []string{
		"update_entity", "delete_entity", "set_attribute", "patch_entity",
		"modify_entity", "drop_entity", "remove_attribute", "put_entity",
	} {
		r := NewRegistry()
		if err := r.Register(wellFormedTool(name)); !errors.Is(err, ErrMutationName) {
			t.Fatalf("registering %q returned %v, want ErrMutationName — a tool named for a verb the store does not have teaches a model a data model this system does not have, and the model then reasons about history and erasure wrongly", name, err)
		}
	}

	// ⚠ False-positive guard. "set" is a substring of "asset", and a substring
	// match would refuse read_asset. A rule that fires on the wrong thing gets
	// switched off, and then it protects nothing.
	r := NewRegistry()
	if err := r.Register(wellFormedTool("read_asset")); err != nil {
		t.Fatalf("read_asset was refused (%v); the mutation check matched a substring rather than a name segment", err)
	}
}

// TestDescriptionCarriesTheRefusals checks the only documentation this caller
// gets says how the tool says no.
func TestDescriptionCarriesTheRefusals(t *testing.T) {
	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}

	for _, tool := range r.Tools() {
		if len(tool.Refusals) == 0 {
			t.Fatalf("tool %q declares no refusals, which registration should have refused", tool.Name)
		}
		described := Describe(tool)
		if !strings.Contains(described, tool.Summary) {
			t.Fatalf("tool %q: the description drops the summary", tool.Name)
		}
		for _, refusal := range tool.Refusals {
			if !strings.Contains(described, refusal) {
				t.Fatalf("tool %q: the description omits the refusal %q — a description that says what a tool does and not what it refuses produces a caller that retries forever", tool.Name, refusal)
			}
		}
		for _, arg := range tool.Args {
			if !strings.Contains(described, arg.Name) {
				t.Fatalf("tool %q: the description omits the argument %q", tool.Name, arg.Name)
			}
		}
	}

	// A tool with no declared refusals cannot be registered in the first place.
	bare := wellFormedTool("read_thing")
	bare.Refusals = nil
	if err := NewRegistry().Register(bare); !errors.Is(err, ErrNoRefusals) {
		t.Fatalf("registering a tool with no refusals returned %v, want ErrNoRefusals", err)
	}
}

// TestStandardToolsAreRegistered is the reachability check: a registry nothing
// populates passes every other test here and offers an agent nothing.
func TestStandardToolsAreRegistered(t *testing.T) {
	declared := Standard()
	if len(declared) == 0 {
		t.Fatal("the surface declares no tools, so an agent that connects finds nothing to call")
	}

	r, err := StandardRegistry()
	if err != nil {
		t.Fatalf("the standard tools do not register: %v", err)
	}
	if got, want := len(r.Tools()), len(declared); got != want {
		t.Fatalf("registry holds %d tools, want %d", got, want)
	}
	for _, tool := range declared {
		if _, ok := r.Lookup(tool.Name); !ok {
			t.Fatalf("tool %q is declared but not registered", tool.Name)
		}
	}
	// Both verbs in the closed set are actually offered; a verb nothing declares
	// is a compile arm nothing exercises.
	seen := map[Verb]bool{}
	for _, tool := range declared {
		seen[tool.Verb] = true
	}
	for _, verb := range []Verb{VerbRead, VerbResemble} {
		if !seen[verb] {
			t.Fatalf("no standard tool declares verb %s, so its compile arm is unreachable in practice", verb)
		}
	}
}
