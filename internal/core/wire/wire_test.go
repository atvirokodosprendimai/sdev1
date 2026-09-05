package wire

import (
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
)

func route(epoch uint64, hops ...string) routing.Route {
	var prefix addr.LeafID
	prefix.Prefix[0] = 0x7f
	prefix.Prefix[1] = 0x21
	prefix.Depth = 2
	return routing.Route{Prefix: prefix, NextHops: hops, Epoch: epoch}
}

// TestARedirectCannotCarryAnAnswer is ADR-043's falsifier.
//
// ⚠ The third assertion is the one that matters. The first two are true of the
// Go struct and prove nothing about a decoder that ignores what it does not
// understand — and "ignore the rest" is exactly how a payload reaches a redirect.
func TestARedirectCannotCarryAnAnswer(t *testing.T) {
	frame, err := Encode(&Redirect{Route: route(42, "node-b", "node-c")})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// It is a redirect, and it is not an answer.
	if _, isAnswer := got.(*Answer); isAnswer {
		t.Fatal("a redirect decoded to an Answer")
	}
	red, ok := got.(*Redirect)
	if !ok {
		t.Fatalf("Decode returned %T, want *Redirect", got)
	}
	if red.Outcome() != OutcomeRedirect {
		t.Errorf("outcome = %v, want redirect", red.Outcome())
	}

	// ★ THE ASSERTION THAT MATTERS: append a payload to a redirect frame and it
	// is REFUSED, not handed back with the extra bytes ignored. A permissive
	// decoder here is how the stale route serves a result.
	smuggled := append(append([]byte(nil), frame...), []byte("datoms-a-caller-would-read")...)
	if _, err := Decode(smuggled); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("a redirect with a payload appended = %v, want ErrTrailingBytes.\n"+
			"\"Ignore what you do not understand\" is precisely how a payload smuggles itself "+
			"into a redirect, and a client that then reads it has been served by the stale "+
			"route it was being redirected away from.", err)
	}
	// And nothing is returned alongside that error.
	if r, _ := Decode(smuggled); r != nil {
		t.Errorf("a refused frame returned %T alongside its error", r)
	}

	// An answer, by contrast, carries its payload — so the redirect's emptiness
	// is a property of that shape rather than of the codec being unable to carry
	// anything at all.
	answer, err := Encode(&Answer{Datoms: []byte("a-datom-run")})
	if err != nil {
		t.Fatalf("Encode(answer): %v", err)
	}
	back, err := Decode(answer)
	if err != nil {
		t.Fatalf("Decode(answer): %v", err)
	}
	if a, ok := back.(*Answer); !ok || string(a.Datoms) != "a-datom-run" {
		t.Errorf("answer round-tripped to %#v", back)
	}
}

// TestAnUnknownOutcomeIsRefused is ADR-043 rules 3 and 7.
func TestAnUnknownOutcomeIsRefused(t *testing.T) {
	frame, err := Encode(&Refusal{Reason: "not the leader"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// An outcome tag from a future version.
	unknown := append([]byte(nil), frame...)
	unknown[2] = 99
	got, err := Decode(unknown)
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("an unknown outcome = %v, want ErrUnknownOutcome.\n"+
			"A decoder that guessed \"probably an answer\" would have rebuilt the flattening "+
			"through the forward-compatibility door.", err)
	}
	// ⚠ And NOTHING is returned. Most callers check the value before the error, so
	// a zero-valued answer beside the error is the flattening with a note attached.
	if got != nil {
		t.Errorf("an unknown outcome returned %T alongside its error", got)
	}

	// The unset tag is not a valid wire value either.
	unset := append([]byte(nil), frame...)
	unset[2] = byte(OutcomeUnset)
	if _, err := Decode(unset); !errors.Is(err, ErrUnknownOutcome) {
		t.Errorf("the unset outcome = %v, want ErrUnknownOutcome", err)
	}

	// An unknown ENVELOPE version is refused the same way, and returns nothing.
	future := append([]byte(nil), frame...)
	future[0], future[1] = 0xff, 0xff
	if r, err := Decode(future); !errors.Is(err, ErrUnknownVersion) || r != nil {
		t.Errorf("an unknown version = %v (%T), want ErrUnknownVersion and no response", err, r)
	}

	// A frame too short to hold a header.
	if r, err := Decode([]byte{0x00}); !errors.Is(err, ErrShortFrame) || r != nil {
		t.Errorf("a short frame = %v (%T), want ErrShortFrame and no response", err, r)
	}
}

// TestTrailingBytesAreRefused is ADR-043 rule 4.
//
// ⚠ Tested for ALL THREE outcomes. Refusing only on a redirect would leave the
// standard extension mechanism open on the shapes beside it, and a payload can
// then reach a client through a frame that was meant to be something else.
func TestTrailingBytesAreRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		r    Response
	}{
		{"answer", &Answer{Datoms: []byte("run")}},
		{"redirect", &Redirect{Route: route(7, "node-b")}},
		{"refusal", &Refusal{Reason: "busy"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			frame, err := Encode(c.r)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			// It decodes cleanly as it is.
			if _, err := Decode(frame); err != nil {
				t.Fatalf("Decode of a clean frame: %v", err)
			}
			// One extra byte is enough.
			got, err := Decode(append(append([]byte(nil), frame...), 0x00))
			if !errors.Is(err, ErrTrailingBytes) {
				t.Errorf("one trailing byte on a %s = %v, want ErrTrailingBytes", c.name, err)
			}
			if got != nil {
				t.Errorf("a %s with trailing bytes returned %T alongside its error", c.name, got)
			}
		})
	}
}

// TestARedirectCarriesItsEpoch is ADR-043 rule 5.
func TestARedirectCarriesItsEpoch(t *testing.T) {
	original := route(1<<40, "node-b", "node-c", "node-a")

	frame, err := Encode(&Redirect{Route: original})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	back := got.(*Redirect).Route

	// ★ The epoch especially: without it a redirect cannot be ordered, and
	// ADR-008 rule 5 is what stops two stale nodes bouncing a client forever.
	if back.Epoch != original.Epoch {
		t.Errorf("epoch = %d, want %d.\n"+
			"A redirect without its epoch still redirects, so nothing fails immediately — "+
			"what fails is the loop protection, later, under exactly the stale-view "+
			"conditions redirecting exists for.", back.Epoch, original.Epoch)
	}

	if back.Prefix != original.Prefix {
		t.Errorf("prefix = %+v, want %+v", back.Prefix, original.Prefix)
	}

	// ⚠ Hop ORDER is part of the route: ADR-008 says two routes with the same
	// hops in a different order are different routes, because a client tries them
	// in sequence.
	if len(back.NextHops) != len(original.NextHops) {
		t.Fatalf("hops = %v, want %v", back.NextHops, original.NextHops)
	}
	for i, want := range original.NextHops {
		if back.NextHops[i] != want {
			t.Errorf("hop %d = %q, want %q — order is part of the route",
				i, back.NextHops[i], want)
		}
	}

	// A route with no hops is still a route: it says the prefix is known and
	// unreachable, which is different from saying nothing.
	empty, err := Encode(&Redirect{Route: route(3)})
	if err != nil {
		t.Fatalf("Encode(no hops): %v", err)
	}
	if r, err := Decode(empty); err != nil {
		t.Errorf("Decode(no hops): %v", err)
	} else if got := r.(*Redirect).Route; got.Epoch != 3 || len(got.NextHops) != 0 {
		t.Errorf("a hopless route round-tripped to %+v", got)
	}
}
