package grid

import "github.com/algoboy-kevin/go-common/market/rowsink"

// overflowList stores closed ranges, coalescing the two oldest into their hull
// on exhaustion. Conservative over-reporting is preferable to losing a drop.
type overflowRange struct{ from, to int64 }
type overflowList struct {
	ranges [4]overflowRange
	n      int
}

func (l *overflowList) add(from, to int64) {
	if l.n == len(l.ranges) {
		a, b := l.ranges[0], l.ranges[1]
		l.ranges[0] = overflowRange{min(a.from, b.from), max(a.to, b.to)}
		copy(l.ranges[1:], l.ranges[2:])
		l.n--
	}
	l.ranges[l.n] = overflowRange{from, to}
	l.n++
}
func (l *overflowList) overlaps(lo, hi int64) bool {
	for i := 0; i < l.n; i++ {
		r := l.ranges[i]
		if r.from <= hi && r.to > lo {
			return true
		}
	}
	return false
}
func (l *overflowList) dropClosed(bound int64) {
	n := 0
	for i := 0; i < l.n; i++ {
		if l.ranges[i].to > bound {
			l.ranges[n] = l.ranges[i]
			n++
		}
	}
	l.n = n
}

// MarkOverflow flags not-yet-emitted market seconds and open/queued funding
// periods intersecting the closed receive-time range. It does not advance time.
// Only the owning shard may call it; unknown instruments are ignored.
func (g *Grid) MarkOverflow(instrument string, fromNs, toNs int64) {
	i, ok := g.index[instrument]
	if !ok || fromNs > toNs {
		return
	}
	in := &g.insts[i]
	// Dropped frames break R25's continuity (P may not be the previous
	// frame, cb/ca may miss prints): clear the tracker so the next bbo
	// frame only seeds. The window's accumulators keep what was received,
	// like the trade bucket; the row is flagged engine_overflow.
	in.micro.clearTracker()
	in.overflow.add(fromNs, toNs)
	if in.fs != nil {
		in.fs.overflow.add(fromNs, toNs)
	}
	for k := 0; k < g.fqueue.count; k++ {
		r := g.fqueue.at(k)
		if r.Instrument == instrument && fromNs <= r.TsS*1e9 && toNs > (r.TsS-r.FundingIntervalS)*1e9 {
			r.SourceFlags |= rowsink.FundFlagEngineOverflow
		}
	}
}
