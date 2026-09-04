package mcpsurface

// Standard returns the tools this engine declares.
//
// ★ Without this, [Registry] is a thing that can hold tools and holds none — it
// would pass every test in this package and offer an agent nothing. A component
// with tests and no caller is the defect that hides best.
//
// ⚠ Both names are read verbs, and both take [TimeArg]. Neither is an accident:
// [Registry.Register] refuses a mutation name and refuses a tool with no time
// argument, so this list is constrained by the same rules it demonstrates.
func Standard() []Tool {
	return []Tool{
		{
			Name:    "read_entity",
			Verb:    VerbRead,
			Summary: "Read attributes of one entity. Time is an argument, not a separate tool: pass as_of to read the entity as it stood at an instant, and leave it out to read it as it stands now.",
			Refusals: []string{
				"an entity that is not named — pass " + EntityArg,
				"an " + TimeArg + " that is not an instant",
				"a session with no tenant; there is no default tenant",
				"a tenant argument has no effect, because the tenant comes from the session",
			},
			Args: []Arg{
				{Name: EntityArg, Required: true, Help: "the entity to read"},
				{Name: "attributes", Required: false, Help: "comma-separated attributes; omit for every attribute"},
				{Name: TimeArg, Required: false, Help: "read the entity as it stood at this instant; omit for now"},
			},
		},
		{
			Name:    "find_resembling",
			Verb:    VerbResemble,
			Summary: "Find subjects resembling one subject on named attributes. An optional attribute that matches nothing yields an unbound value and keeps the row; a required one that matches nothing drops it.",
			Refusals: []string{
				"a threshold that is not a number — there is no default, because a default makes the result reproducible only by whoever knows it",
				"a subject that is not named — pass " + EntityArg,
				"an " + TimeArg + " that is not an instant",
				"a session with no tenant; there is no default tenant",
			},
			Args: []Arg{
				{Name: EntityArg, Required: true, Help: "the subject to resemble"},
				{Name: "require", Required: false, Help: "comma-separated attributes that must match"},
				{Name: "optional", Required: false, Help: "comma-separated attributes that may match; a miss binds nothing and keeps the row"},
				{Name: "metric", Required: true, Help: "the similarity metric, stated rather than defaulted"},
				{Name: "threshold", Required: true, Help: "the similarity threshold, stated rather than defaulted"},
				{Name: TimeArg, Required: false, Help: "resemble as of this instant; omit for now"},
			},
		},
	}
}

// StandardRegistry returns a registry holding [Standard].
func StandardRegistry() (*Registry, error) {
	r := NewRegistry()
	for _, t := range Standard() {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}
	return r, nil
}
