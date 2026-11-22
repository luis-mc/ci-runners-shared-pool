package forecast

import "sort"

// SlotJob is a running job (Remaining is what is left of its forecast) or a queued job (the whole forecast).
// Lo, P50 and Hi of Remaining are used.
type SlotJob struct {
	Remaining Forecast
	Slot      int // 1-based slot a running job holds, 0 when not known; only SimulateQueueSlots reads it
}

// SimulateQueue gives, for each queued job in queue order, the seconds from now until it starts, as lows, medians
// and highs (three parallel simulations). Each running job frees its slot at its remaining forecast; with more
// running jobs than slots the earliest ends only bring the running count down to the slots. Each queued job takes
// the first free slot and holds it for its own forecast. known[i] is false when the start cannot be told: no slot
// at all (slots < 1), a running job without history, or a queued job without history ahead of it. The Eta is then
// the zero Eta.
func SimulateQueue(slots int, running, queued []SlotJob) (etas []Eta, known []bool) {
	etas, known = make([]Eta, len(queued)), make([]bool, len(queued))
	if slots < 1 {
		return etas, known
	}
	ok := true
	for _, r := range running {
		if r.Remaining.Basis == BasisNone {
			ok = false
		}
	}
	if !ok {
		return etas, known
	}
	picks := [3]func(Forecast) float64{
		func(f Forecast) float64 { return f.Lo },
		func(f Forecast) float64 { return f.P50 },
		func(f Forecast) float64 { return f.Hi },
	}
	var starts [3][]float64
	for k, pick := range picks {
		ends := make([]float64, 0, len(running))
		for _, r := range running {
			ends = append(ends, pick(r.Remaining))
		}
		sort.Float64s(ends)
		pool := make([]float64, slots)
		if len(ends) > slots {
			copy(pool, ends[len(ends)-slots:])
		} else {
			copy(pool[slots-len(ends):], ends) // free slots are the zeros at the front
		}
		starts[k] = make([]float64, len(queued))
		for i, q := range queued {
			sort.Float64s(pool)
			starts[k][i] = pool[0]
			pool[0] += pick(q.Remaining)
		}
	}
	for i, q := range queued {
		if !ok {
			break
		}
		etas[i] = Eta{Lo: starts[0][i], P50: starts[1][i], Hi: starts[2][i]}
		known[i] = true
		if q.Remaining.Basis == BasisNone {
			ok = false
		}
	}
	return etas, known
}

// SimulateQueueSlots is SimulateQueue that also tells, for each queued job, the 1-based slot it takes in the median
// simulation (0 when its start is not known). A running job with Slot in 1..slots keeps that slot; the others take
// the free slot numbers in order. Ties go to the lower slot number, so the answer is deterministic.
func SimulateQueueSlots(slots int, running, queued []SlotJob) (etas []Eta, known []bool, slot []int) {
	etas, known = SimulateQueue(slots, running, queued)
	slot = make([]int, len(queued))
	if slots < 1 {
		return etas, known, slot
	}
	free := make([]float64, slots+1) // index 1..slots: when that slot frees, by the median
	order := append([]SlotJob(nil), running...)
	sort.SliceStable(order, func(a, b int) bool { return order[a].Remaining.P50 < order[b].Remaining.P50 })
	if len(order) > slots {
		order = order[len(order)-slots:] // the earliest ends only bring the count down, as in SimulateQueue
	}
	taken := make([]bool, slots+1)
	var loose []SlotJob
	for _, r := range order {
		if r.Slot >= 1 && r.Slot <= slots && !taken[r.Slot] {
			taken[r.Slot], free[r.Slot] = true, r.Remaining.P50
		} else {
			loose = append(loose, r)
		}
	}
	next := 1
	for _, r := range loose {
		for taken[next] {
			next++
		}
		taken[next], free[next] = true, r.Remaining.P50
	}
	for i, q := range queued {
		best := 1
		for n := 2; n <= slots; n++ {
			if free[n] < free[best] {
				best = n
			}
		}
		if known[i] {
			slot[i] = best
		}
		free[best] += q.Remaining.P50
	}
	return etas, known, slot
}
