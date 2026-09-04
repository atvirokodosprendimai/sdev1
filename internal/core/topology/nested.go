package topology

// number walks the authored tree depth-first, assigning each node the
// nested-set interval [Lft, Rgt]: Lft on entry, Rgt on exit.
//
// The result is that one node contains another exactly when its interval
// encloses the other's, so every later ancestry question is an integer
// comparison rather than a traversal. Nodes come back in entry order, which is
// ascending Lft, so the slice is already sorted.
func number(root AuthoredNode, levelOf map[string]int) []Node {
	nodes := make([]Node, 0, 16)
	counter := 0
	var walk func(AuthoredNode)
	walk = func(a AuthoredNode) {
		counter++
		i := len(nodes)
		nodes = append(nodes, Node{
			Name:     a.Name,
			LevelIdx: levelOf[a.Level],
			Lft:      counter,
			Weight:   a.Weight,
		})
		for _, c := range a.Children {
			walk(c)
		}
		counter++
		nodes[i].Rgt = counter
	}
	walk(root)
	return nodes
}

// Tree rebuilds the authored nested form from the resident intervals.
//
// It is the inverse of the numbering [Load] performs, and the two are asserted
// to round-trip by a property test over generated trees. Having exactly one path
// in each direction is deliberate: a value that flows through two
// representations acquires a defect class where a check exercising one path
// cannot see the other narrowing what it carries.
//
// Tree is also what an operator-facing view renders, since the nested form is
// the one a person can read.
func (m Map) Tree() AuthoredNode {
	if len(m.Nodes) == 0 {
		return AuthoredNode{}
	}
	built := make([]AuthoredNode, len(m.Nodes))
	for i, n := range m.Nodes {
		built[i] = AuthoredNode{
			Level:  m.Levels[n.LevelIdx],
			Name:   n.Name,
			Weight: n.Weight,
		}
	}
	// Attach children to parents from the deepest index back, so a node's own
	// Children slice is complete before it is attached to its parent. Prepending
	// preserves authored order, because a parent's children were numbered in
	// ascending index order.
	parent := m.parents()
	for i := len(m.Nodes) - 1; i >= 1; i-- {
		p := parent[i]
		if p < 0 {
			continue
		}
		built[p].Children = append([]AuthoredNode{built[i]}, built[p].Children...)
	}
	return built[0]
}

// parents derives each node's parent index from the intervals alone, returning
// -1 for the root.
//
// Because Nodes is in depth-first entry order, a stack of still-open intervals
// names the current parent at every step — no parent pointer is stored, which is
// what keeps the resident form flat and pointer-free.
func (m Map) parents() []int {
	parent := make([]int, len(m.Nodes))
	var stack []int
	for i, n := range m.Nodes {
		for len(stack) > 0 && m.Nodes[stack[len(stack)-1]].Rgt < n.Lft {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			parent[i] = -1
		} else {
			parent[i] = stack[len(stack)-1]
		}
		stack = append(stack, i)
	}
	return parent
}
