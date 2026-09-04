package observe

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrNoQuestion reports a counter registered without the operator question it
// answers.
//
// ★ Not a description of what it counts — the NAME says that. The question is
// what an operator would settle by looking, and a counter whose question cannot
// be written is one nobody needs. Writing it is where that becomes obvious,
// which is why the rule lives at registration rather than in a cleanup that
// never happens.
var ErrNoQuestion = errors.New("observe: a counter must state the operator question it answers")

// ErrDuplicateCounter reports two registrations of one name.
var ErrDuplicateCounter = errors.New("observe: a counter with that name is already registered")

// Counter counts something, and says why.
type Counter struct {
	// Name is what is counted.
	Name string
	// Question is what an operator settles by reading it.
	Question string

	value atomic.Uint64
}

// Add increments the counter.
func (c *Counter) Add(n uint64) { c.value.Add(n) }

// Value reads it.
func (c *Counter) Value() uint64 { return c.value.Load() }

var (
	countersMu sync.RWMutex
	counters   = map[string]*Counter{}
)

// RegisterCounter creates a counter, refusing one with no stated question.
func RegisterCounter(name, question string) (*Counter, error) {
	if name == "" {
		return nil, fmt.Errorf("observe: a counter needs a name")
	}
	if question == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoQuestion, name)
	}

	countersMu.Lock()
	defer countersMu.Unlock()
	if _, ok := counters[name]; ok {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateCounter, name)
	}
	c := &Counter{Name: name, Question: question}
	counters[name] = c
	return c, nil
}

// Counters returns every counter WITH its question, ordered by name.
//
// ★ Returning the question alongside the number is the point: a reader of the
// list learns why each one is there, which is what stops the list filling with
// numbers nobody can justify.
func Counters() []*Counter {
	countersMu.RLock()
	defer countersMu.RUnlock()
	out := make([]*Counter, 0, len(counters))
	for _, c := range counters {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CounterNamed returns one counter.
func CounterNamed(name string) (*Counter, bool) {
	countersMu.RLock()
	defer countersMu.RUnlock()
	c, ok := counters[name]
	return c, ok
}

// droppedEvents counts what the stream could not take.
//
// ⚠ It is itself a declared counter with its own question, because a count that
// reveals lost events must not be another unread number.
var droppedEvents *Counter

func init() {
	c, err := RegisterCounter("observe.events_dropped",
		"is the event stream losing events under load, and therefore hiding the very load an "+
			"operator is investigating?")
	if err != nil {
		panic("observe: registering the drop counter: " + err.Error())
	}
	droppedEvents = c
}
