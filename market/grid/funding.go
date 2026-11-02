package grid

// funding_settlement aggregation (docs/DATA_POINTS.md 2.3, rules R16-R20).
//
// One row per instrument per funding settlement. A scheduled instrument
// (rule R10 IntervalS > 0) accumulates the period's activeAssetCtx frames
// into the open period (S-I, S]; periods close on data frames and Flush
// only (never on _meta, like R2), become pending rows in a FIFO queue, and
// are emitted through rowsink.FundingSink once their settled value resolves
// (rule R18/R20). Output order is (ts_s, universe) (rule R15).
//
// Float determinism: the only product here is oi*mark, wrapped in an
// explicit float64(...) like every product that feeds a sum elsewhere in
// the package (see the package comment in grid.go).

import (
	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// maxFundGaps bounds one instrument's funding gap list (rule R11 windows
// over whole funding periods, so it holds far more than the 1 s grid's
// 4-slot lists and is trimmed only at period close).
const maxFundGaps = 32

// fundQueueCap is the preallocated pending-row ring of the settled queue
// (rule R20). A Pending head can only hold up to settled_wait_s of rows
// back, so the ring overflows only under a dead settled source.
const fundQueueCap = 256

// noCloseSentinel stands for "no scheduled instrument has an open period".
const noCloseSentinel = int64(1) << 62

// fundGapList is the per-instrument funding gap list: the same R11 gap
// state machine as gapList but with capacity 32, never trimmed per second,
// and an eviction result the caller must evaluate (the grid's own gap lists
// must not change behaviour, so this is a second type on purpose).
type fundGapList struct {
	gaps [maxFundGaps]gap
	n    int
}

// openAt records a disconnected event and returns the evicted gap when the
// list was full (oldest evicted, as in gapList).
func (l *fundGapList) openAt(ns int64) (evicted gap, evictedOK bool) {
	if l.n == maxFundGaps {
		evicted, evictedOK = l.gaps[0], true
		copy(l.gaps[:], l.gaps[1:])
		l.n--
	}
	l.gaps[l.n] = gap{openNs: ns}
	l.n++
	return evicted, evictedOK
}

// resubAt records the next resubscribed event (see gapList.resubAt).
func (l *fundGapList) resubAt(ns, limitNs int64) {
	for i := 0; i < l.n; i++ {
		g := &l.gaps[i]
		if !g.hasResub {
			g.hasResub = true
			g.resubNs = ns
			g.closeNs = ns + limitNs
		}
	}
}

// noteFrame lowers the close of a gap to the first data frame after the
// resubscribed event (see gapList.noteFrame).
func (l *fundGapList) noteFrame(rxNs int64) {
	for i := 0; i < l.n; i++ {
		g := &l.gaps[i]
		if g.hasResub && rxNs > g.resubNs && (g.closeNs == 0 || rxNs < g.closeNs) {
			g.closeNs = rxNs
		}
	}
}

// dropClosed removes gaps with close <= boundNs (see gapList.dropClosed).
func (l *fundGapList) dropClosed(boundNs int64) {
	w := 0
	for i := 0; i < l.n; i++ {
		g := l.gaps[i]
		if g.closeNs != 0 && g.closeNs <= boundNs {
			continue
		}
		l.gaps[w] = g
		w++
	}
	l.n = w
}

// overlapsWindow reports whether any gap overlaps the period window
// [lo, hi] (rule R19 flag 8: "tested exactly like R11 with a window of I
// seconds"): open <= hi && (close unknown or close > lo), degenerate gaps
// (close <= open) skipped, unknown close counted as +inf.
func (l *fundGapList) overlapsWindow(lo, hi int64) bool {
	for i := 0; i < l.n; i++ {
		if gapOverlapsWindow(&l.gaps[i], lo, hi) {
			return true
		}
	}
	return false
}

func gapOverlapsWindow(g *gap, lo, hi int64) bool {
	if g.closeNs != 0 && g.closeNs <= g.openNs {
		return false // degenerate empty gap never flags
	}
	return g.openNs <= hi && (g.closeNs == 0 || g.closeNs > lo)
}

// fundAcc accumulates the funding_settlement row of one open period
// (S-I, S], rules R16/R17. A field a frame did not carry (feed contract
// presence bit) is skipped for that field's aggregates, so every aggregate
// counts its own contributing frames (nF, nP) and is null when none
// contributed. Zeroed in place after each closed period.
type fundAcc struct {
	n int64 // ctx frames in the period (ctx_frames)

	nF                int64
	fSum              float64
	fMin, fMax, fLast float64
	fPrev             float64
	fPrevOk           bool
	fChanges          int64

	nP         int64
	pSum       float64
	pLast      float64
	pLastOk    bool
	oLast      float64
	oLastOk    bool
	mLast      float64
	mLastOk    bool
	oiStart    float64
	oiStartOk  bool
	oiEnd      float64
	oiEndOk    bool
	prevRx     int64
	prevRxOk   bool
	gapMax     int64 // running ctx_gap_max_ms (floor((later-earlier)/1e6))
	feedSticky bool  // a funding gap evicted from the full list flagged the period
}

// fundState is the funding state of one scheduled instrument: the open
// period end (curS), its accumulator and its own ctx-channel gap list
// (rule R11 over period windows).
type fundState struct {
	I            int64 // schedule IntervalS (rule R10)
	curS         int64 // open period end, aligned to (AnchorS mod I)
	open         bool  // curS is initialised
	pendingEvict bool  // gap evicted before the first period opened
	acc          fundAcc
	gaps         fundGapList
	overflow     overflowList
}

// addCtx accumulates one ctx frame of the open period (rule R17). Frames
// arrive in R1 order and the caller has closed every period with S*1e9 <
// rx, so the frame always lands in the open period.
func (fs *fundState) addCtx(rxNs int64, f *feed.AssetCtx) {
	a := &fs.acc
	a.n++
	// ctx_gap_max_ms edges (rule R17): the start edge (S-I)*1e9 -> first
	// frame is added here, the final edge -> S*1e9 at period close.
	if a.prevRxOk {
		if d := (rxNs - a.prevRx) / 1e6; d > a.gapMax {
			a.gapMax = d
		}
	} else {
		a.gapMax = (rxNs - (fs.curS-fs.I)*1e9) / 1e6
	}
	a.prevRx, a.prevRxOk = rxNs, true

	h := f.Have
	if h&feed.CtxFunding != 0 {
		v := f.Funding
		a.fSum += v // sequential frame-order sum (rule R17)
		a.nF++
		if !a.fPrevOk {
			a.fMin, a.fMax = v, v
		} else {
			if v != a.fPrev {
				a.fChanges++
			}
			if v < a.fMin {
				a.fMin = v
			}
			if v > a.fMax {
				a.fMax = v
			}
		}
		a.fPrev, a.fPrevOk = v, true
		a.fLast = v
	}
	if h&feed.CtxPremium != 0 {
		v := f.Premium
		a.pSum += v
		a.nP++
		a.pLast, a.pLastOk = v, true
	}
	if h&feed.CtxOraclePx != 0 {
		a.oLast, a.oLastOk = f.OraclePx, true
	}
	if h&feed.CtxMarkPx != 0 {
		a.mLast, a.mLastOk = f.MarkPx, true
	}
	if h&feed.CtxOpenInterest != 0 && h&feed.CtxMarkPx != 0 {
		u := float64(f.OpenInterest * f.MarkPx) // one product (FMA barrier)
		if !a.oiStartOk {
			a.oiStart, a.oiStartOk = u, true
		}
		a.oiEnd, a.oiEndOk = u, true
	}
}

// fundQueue is the preallocated FIFO of closed-but-unemitted rows (rule
// R20): rows are pushed in (ts_s, universe) order and only the head is
// examined, so emission order is the rule R15 order. The ring grows only
// when a dead settled source blocks the head for longer than settled_wait_s
// allows back; steady state allocates nothing.
type fundQueue struct {
	ring  []rowsink.FundingSettlement
	head  int
	count int
}

func (q *fundQueue) empty() bool { return q.count == 0 }

func (q *fundQueue) at(off int) *rowsink.FundingSettlement {
	return &q.ring[(q.head+off)%len(q.ring)]
}

func (q *fundQueue) front() *rowsink.FundingSettlement { return q.at(0) }

func (q *fundQueue) push() *rowsink.FundingSettlement {
	if q.count == len(q.ring) {
		grown := make([]rowsink.FundingSettlement, 2*len(q.ring)+fundQueueCap)
		for i := 0; i < q.count; i++ {
			grown[i] = *q.at(i)
		}
		q.ring, q.head = grown, 0
	}
	q.count++
	return q.at(q.count - 1)
}

func (q *fundQueue) pop() {
	q.head = (q.head + 1) % len(q.ring)
	q.count--
}

// EnableFunding turns on funding_settlement output (off until called):
// closed periods are queued and emitted to fs once SettledSource resolves
// them (rules R16-R20). src must not be nil. Call it before the first data
// frame; per-instrument funding state is allocated here, and steady state
// allocates nothing.
func (g *Grid) EnableFunding(fs rowsink.FundingSink, src SettledSource) {
	g.fundSink, g.fundSrc = fs, src
	if g.fundOn {
		return
	}
	g.fundOn = true
	g.fnextCloseNs = noCloseSentinel
	g.fqueue.ring = make([]rowsink.FundingSettlement, fundQueueCap)
	for i := range g.insts {
		in := &g.insts[i]
		if in.fund.IntervalS > 0 {
			in.fs = &fundState{I: in.fund.IntervalS}
			g.fundAny = true
		}
	}
}

// DrainFunding force-emits everything still queued at shutdown: a head the
// settled source resolves is emitted with its value, an unresolved one with
// SETTLED_NOT_FETCHED (rule R20). Open periods produce no row (rule R16);
// a settled value arriving later is never re-emitted.
func (g *Grid) DrainFunding() {
	if !g.fundOn {
		return
	}
	g.drainFunding(0, true)
}

// fundStart initialises every scheduled instrument's open period at the
// first data frame of the slice (minRx): curS is the smallest settlement
// second S with S*1e9 >= minRx (rules R16/R2).
func (g *Grid) fundStart(minRxNs int64) {
	if !g.fundOn {
		return
	}
	c := ceilSec(minRxNs)
	for i := range g.insts {
		in := &g.insts[i]
		fs := in.fs
		if fs == nil {
			continue
		}
		I, A := in.fund.IntervalS, in.fund.AnchorS
		m := (A - c) % I // posmod: smallest non-negative ((A-c) mod I)
		if m < 0 {
			m += I
		}
		fs.curS, fs.open = c+m, true
		if fs.pendingEvict {
			// Folded into the first period: see fundOpenAt.
			fs.acc.feedSticky = true
			fs.pendingEvict = false
		}
	}
	g.fundRecalcNext()
}

// fundRecalcNext sets fnextCloseNs to the smallest open period end.
func (g *Grid) fundRecalcNext() {
	g.fnextCloseNs = noCloseSentinel
	for i := range g.insts {
		if fs := g.insts[i].fs; fs != nil && fs.open && fs.curS*1e9 < g.fnextCloseNs {
			g.fnextCloseNs = fs.curS * 1e9
		}
	}
}

// fundingAdvance closes every open period that ended before nowNs (or at
// nowNs when inclusive, for Flush) and then resolves the settled queue.
// Data frames close with S*1e9 < rx and Flush with S*1e9 <= untilNs, so
// exactly the rows of rules R16 are produced and a trailing open period
// never emits. fnextCloseNs keeps a normal frame from scanning the
// universe.
func (g *Grid) fundingAdvance(nowNs int64, inclusive bool) {
	if !g.fundOn {
		return
	}
	qualifies := func(n int64) bool {
		if inclusive {
			return n <= nowNs
		}
		return n < nowNs
	}
	if g.fundAny {
		for qualifies(g.fnextCloseNs) {
			// Close the smallest (curS, universe index) first: rows must be
			// queued in (ts_s, universe) order (rule R15/R20).
			bi, minS := -1, int64(0)
			for i := range g.insts {
				fs := g.insts[i].fs
				if fs == nil || !fs.open {
					continue
				}
				if bi < 0 || fs.curS < minS {
					bi, minS = i, fs.curS
				}
			}
			if bi < 0 {
				g.fnextCloseNs = noCloseSentinel
				break
			}
			g.fnextCloseNs = minS * 1e9
			if !qualifies(g.fnextCloseNs) {
				break
			}
			g.closeFundPeriod(bi, nowNs)
		}
	}
	g.drainFunding(nowNs, false)
}

// closeFundPeriod closes the open period of one instrument: it becomes a
// queued row unless its end equals minRx (rule R16: such a period produces
// no row and its frames are not counted), then the accumulator resets and
// the gap list is trimmed to the lo of the next period (a gap with close <=
// S*1e9 can never overlap a later period again). The queue is drained right
// after the push: a complete settled source resolves every row at close.
func (g *Grid) closeFundPeriod(i int, nowNs int64) {
	in := &g.insts[i]
	fs := in.fs
	S := fs.curS
	if S*1e9 > g.minRxNs {
		a := &fs.acc
		r := g.fqueue.push()
		*r = rowsink.FundingSettlement{}
		r.TsS = S
		r.Instrument = in.name
		r.FundingIntervalS = fs.I
		r.CtxFrames = a.n
		if a.nF > 0 {
			r.FundingCtxLast = rowsink.Some(a.fLast)
			r.FundingCtxMean = rowsink.Some(a.fSum / float64(a.nF))
			r.FundingCtxMin = rowsink.Some(a.fMin)
			r.FundingCtxMax = rowsink.Some(a.fMax)
		}
		r.FundingChanges = a.fChanges
		if a.pLastOk {
			r.PremiumMean = rowsink.Some(a.pSum / float64(a.nP))
			r.PremiumLast = rowsink.Some(a.pLast)
		}
		if a.oLastOk {
			r.OraclePxSettle = rowsink.Some(a.oLast)
		}
		if a.mLastOk {
			r.MarkPxSettle = rowsink.Some(a.mLast)
		}
		if a.oiStartOk {
			r.OIUSDStart = rowsink.Some(a.oiStart)
		}
		if a.oiEndOk {
			r.OIUSDEnd = rowsink.Some(a.oiEnd)
		}
		if a.n > 0 {
			m := a.gapMax
			if d := (S*1e9 - a.prevRx) / 1e6; d > m {
				m = d
			}
			r.CtxGapMaxMs = rowsink.SomeI(m)
		}
		var flags uint16
		if g.minRxNs > (S-fs.I)*1e9 {
			flags |= rowsink.FundFlagPartialStart // rule R19 bit 4
		}
		if a.feedSticky || fs.gaps.overlapsWindow((S-fs.I)*1e9, S*1e9) {
			flags |= rowsink.FundFlagFeedGap // rule R19 bit 8
		}
		if a.n == 0 {
			flags |= rowsink.FundFlagNoCtx // rule R19 bit 16
		}
		if fs.overflow.overlaps((S-fs.I)*1e9, S*1e9) {
			flags |= rowsink.FundFlagEngineOverflow
		}
		r.SourceFlags = flags
		g.drainFunding(nowNs, false) // resolved rows emit right at close
	}
	fs.acc = fundAcc{}
	fs.curS += fs.I
	fs.overflow.dropClosed(S * 1e9)
	fs.gaps.dropClosed(S * 1e9) // lo of the next period
}

// drainFunding emits queued rows while the head resolves (rule R20). A
// Pending head blocks the rows behind it (head-of-line) until its settled
// value arrives or its deadline (S + settled_wait_s of frame time) passes;
// force (shutdown drain) emits it immediately. Only the head is examined.
func (g *Grid) drainFunding(nowNs int64, force bool) {
	for !g.fqueue.empty() {
		q := g.fqueue.front()
		rate, st := g.fundSrc.Settled(q.Instrument, q.TsS)
		settled := rowsink.F64{}
		switch st {
		case SettledFound:
			settled = rowsink.Some(rate)
		case SettledMissing:
			q.SourceFlags |= rowsink.FundFlagSettledMissing // rule R19 bit 2
		case SettledNotFetched:
			q.SourceFlags |= rowsink.FundFlagSettledNotFetched // rule R19 bit 1
		case SettledPending:
			if force || nowNs > q.TsS*1e9+g.cfg.SettledWaitS*1e9 {
				q.SourceFlags |= rowsink.FundFlagSettledNotFetched
			} else {
				return
			}
		}
		g.emitFundRow(q, settled)
		g.fqueue.pop()
	}
}

// emitFundRow copies a queued row into the reused output struct, adds the
// settled value and reconcile_diff (rule R20: "is non-null (0.0 included)
// whenever both rates are non-null"; flag 32 on an exact float mismatch),
// and passes it to the sink by pointer.
func (g *Grid) emitFundRow(q *rowsink.FundingSettlement, settled rowsink.F64) {
	g.fRow = *q
	g.fRow.FundingSettled = settled
	if settled.Ok && g.fRow.FundingCtxLast.Ok {
		g.fRow.ReconcileDiff = rowsink.Some(g.fRow.FundingCtxLast.V - settled.V)
		if g.fRow.FundingCtxLast.V != settled.V {
			g.fRow.SourceFlags |= rowsink.FundFlagReconcileMismatch // bit 32
		}
	}
	g.fundSink.FundingSettlement(&g.fRow)
}

// fundOpenAt feeds a ctx-channel disconnected event into one instrument's
// funding gap list (rule R11/R19 bit 8). On eviction the gap is evaluated
// against the open period first (unknown close counts as +inf) and folded
// into its sticky feed-gap flag.
func (g *Grid) fundOpenAt(i int, ns int64) {
	fs := g.insts[i].fs
	if fs == nil {
		return
	}
	evicted, evictedOK := fs.gaps.openAt(ns)
	if !evictedOK {
		return
	}
	if fs.open {
		if gapOverlapsWindow(&evicted, (fs.curS-fs.I)*1e9, fs.curS*1e9) {
			fs.acc.feedSticky = true
		}
	} else {
		// No period is open before the first data frame; fold the eviction
		// into the first period (errs toward flagging, never silent loss).
		fs.pendingEvict = true
	}
}

// fundResubAt feeds a ctx-channel resubscribed event into one instrument's
// funding gap list with the channel's R11 closing-(b) limit.
func (g *Grid) fundResubAt(i int, ns, limitNs int64) {
	if fs := g.insts[i].fs; fs != nil {
		fs.gaps.resubAt(ns, limitNs)
	}
}
