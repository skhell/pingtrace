package probe

import (
	"context"
	"math"
	"time"
)

// MTRResult is the accumulated MTR state, suitable for live render
// or final snapshot.
type MTRResult struct {
	Target string    `json:"target"`
	Cycles int       `json:"cycles"`
	Hops   []MTRStat `json:"hops"`
}

// MTRStat is per-hop running stats in classic mtr column order.
type MTRStat struct {
	Hop    int     `json:"hop"`
	IP     string  `json:"ip"`
	LossPc float64 `json:"loss"`
	Snt    int     `json:"snt"`
	Last   float64 `json:"last"`
	Avg    float64 `json:"avg"`
	Best   float64 `json:"best"`
	Wrst   float64 `json:"wrst"`
	StDev  float64 `json:"stdev"`

	// Welford internal state.
	m2  float64
	rcv int
}

// MTRUpdate is emitted on the channel after each completed cycle.
type MTRUpdate struct {
	Cycle    int
	Snapshot MTRResult
}

// MTR runs traceroute every `interval` until ctx is cancelled or
// `cycles` is reached (0 = infinite). It emits an MTRUpdate on the
// returned channel after each cycle.
func MTR(ctx context.Context, target string, cycles int, interval time.Duration, traceOpts TraceOptions) <-chan MTRUpdate {
	ch := make(chan MTRUpdate, 2)
	if interval <= 0 {
		interval = 1 * time.Second
	}

	go func() {
		defer close(ch)
		state := MTRResult{Target: target}
		for c := 1; cycles == 0 || c <= cycles; c++ {
			tr, err := Trace(ctx, target, traceOpts)
			if err == nil {
				mergeCycle(&state, tr)
			}
			state.Cycles = c
			select {
			case ch <- MTRUpdate{Cycle: c, Snapshot: cloneMTR(state)}:
			case <-ctx.Done():
				return
			}
			if cycles != 0 && c >= cycles {
				return
			}
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

func mergeCycle(state *MTRResult, tr TraceResult) {
	// Grow state.Hops to cover all observed TTLs.
	for _, h := range tr.Hops {
		for len(state.Hops) < h.Hop {
			state.Hops = append(state.Hops, MTRStat{Hop: len(state.Hops) + 1})
		}
		st := &state.Hops[h.Hop-1]
		st.Snt++
		if h.Status != "ok" {
			st.LossPc = float64(st.Snt-st.rcv) / float64(st.Snt) * 100
			continue
		}
		if st.IP == "" {
			st.IP = h.IP
		}
		t := h.TimeMs
		st.Last = t
		st.rcv++
		// Welford
		prevAvg := st.Avg
		st.Avg += (t - prevAvg) / float64(st.rcv)
		st.m2 += (t - prevAvg) * (t - st.Avg)
		if st.rcv == 1 || t < st.Best {
			st.Best = t
		}
		if t > st.Wrst {
			st.Wrst = t
		}
		if st.rcv >= 2 {
			st.StDev = math.Sqrt(st.m2 / float64(st.rcv-1))
		}
		st.LossPc = float64(st.Snt-st.rcv) / float64(st.Snt) * 100
	}
}

func cloneMTR(in MTRResult) MTRResult {
	out := MTRResult{Target: in.Target, Cycles: in.Cycles}
	out.Hops = make([]MTRStat, len(in.Hops))
	copy(out.Hops, in.Hops)
	return out
}
