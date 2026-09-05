package admit

// Class is what a read is FOR, and therefore what shedding it costs.
//
// ★ It orders what a saturated node gives up. It is NOT a second budget: there
// is still one read ceiling and one read utilisation, and both classes move the
// same number. `BACKLOG.md` §22 forbids a budget per class, and a per-class
// ceiling would be the third budget kind this package already refused — priority
// WITHIN a budget is a different mechanism, and this is that mechanism.
//
// ⚠ The order comes from ELASTICITY, not from importance, and it is the same
// argument [Kind] already uses one level up: a read is shed and a write is not
// because any replica can serve a read, so shedding it RE-ROUTES the work, while
// a leaf has one writer, so shedding a write is an outage. Applied to the two
// read classes, that same property separates them — see [ClassRepair].
type Class int

const (
	// ClassUnset is the zero value and is never valid.
	ClassUnset Class = iota

	// ClassUser: a read answering a caller.
	//
	// ★ It is ELASTIC. Any replica holds the data, so shedding it re-routes the
	// read to a peer — which is what admission control is for.
	ClassUser

	// ClassRepair: a read rebuilding what is missing.
	//
	// ⚠ It is NOT elastic. It reads the fragments THIS node holds, so shedding it
	// does not move the work anywhere — it cancels it, which is precisely what
	// this package says makes a shed write an outage rather than a re-route.
	//
	// ★ A second reason points the same way: a degraded read costs k fragment
	// fetches, so the reads a repair makes unnecessary are also the expensive
	// ones. Shedding repair prolongs the degradation generating the load.
	ClassRepair
)

func (c Class) String() string {
	switch c {
	case ClassUser:
		return "user"
	case ClassRepair:
		return "repair"
	default:
		return "unset"
	}
}

// Classes returns the two classes in the order they are SHED, first shed first.
//
// ★ The order is returned rather than left to be inferred from a comparison
// somewhere, so a reader learns it from the API instead of from an implementation
// detail that a refactor could invert without anyone noticing.
func Classes() []Class { return []Class{ClassUser, ClassRepair} }

// sheddable reports whether a withdrawn node gives this class up.
//
// ⚠ Only [ClassUser]. A withdrawn node keeps serving repair reads because there is
// nowhere to shed them TO: see [ClassRepair].
func (c Class) sheddable() bool { return c == ClassUser }
