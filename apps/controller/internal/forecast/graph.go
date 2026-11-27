package forecast

// Node is a job in a run's needs graph. Remaining is 0 for a finished node (any Basis but BasisNone), the forecast
// minus the elapsed time for a running one (RemainingAfter), the whole forecast for a queued one. Only Remaining
// with Basis BasisNone counts as unknown; it adds 0 seconds.
type Node struct {
	ID        string
	Needs     []string
	Remaining Forecast
}

func topoOrder(nodes []Node) []Node {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	seen := make(map[string]bool, len(nodes))
	out := make([]Node, 0, len(nodes))
	var visit func(n Node, stack map[string]bool)
	visit = func(n Node, stack map[string]bool) {
		if seen[n.ID] || stack[n.ID] {
			return
		}
		stack[n.ID] = true
		for _, need := range n.Needs {
			if dep, ok := byID[need]; ok {
				visit(dep, stack)
			}
		}
		delete(stack, n.ID)
		seen[n.ID] = true
		out = append(out, n)
	}
	for _, n := range nodes {
		visit(n, map[string]bool{})
	}
	return out
}

// longest is the longest path over the graph for one duration: its node ids and its total. On a tie the later
// node wins, so an unknown node (0 s) after the path is not hidden.
func longest(order []Node, pick func(Forecast) float64) (path []string, total float64) {
	finish := make(map[string]float64, len(order))
	via := make(map[string]string, len(order))
	last, haveLast := "", false
	for _, n := range order {
		start, from, hasFrom := 0.0, "", false
		for _, need := range n.Needs {
			if f, ok := finish[need]; ok && f >= start {
				start, from, hasFrom = f, need, true
			}
		}
		end := start + pick(n.Remaining)
		finish[n.ID] = end
		if hasFrom {
			via[n.ID] = from
		}
		if !haveLast || end >= total {
			last, total, haveLast = n.ID, end, true
		}
	}
	if !haveLast {
		return nil, 0
	}
	for at := last; ; {
		path = append([]string{at}, path...)
		from, ok := via[at]
		if !ok {
			break
		}
		at = from
	}
	return path, total
}

// CriticalPath finds the longest path over the needs graph (a cycle or a missing need is ignored). path is the
// longest path by median; eta.P50 is its length, eta.Lo and eta.Hi the longest paths by p25 and by p75. complete is
// false when a node on the path has no history (BasisNone). An empty graph gives no path, zero eta, complete.
func CriticalPath(nodes []Node) (path []string, eta Eta, complete bool) {
	order := topoOrder(nodes)
	path, eta.P50 = longest(order, func(f Forecast) float64 { return f.P50 })
	_, eta.Lo = longest(order, func(f Forecast) float64 { return f.P25 })
	_, eta.Hi = longest(order, func(f Forecast) float64 { return f.P75 })
	unknown := make(map[string]bool)
	for _, n := range nodes {
		if n.Remaining.Basis == BasisNone {
			unknown[n.ID] = true
		}
	}
	complete = true
	for _, id := range path {
		if unknown[id] {
			complete = false
		}
	}
	return path, eta, complete
}
