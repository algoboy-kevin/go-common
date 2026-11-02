package grid

import (
	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// Channel slots for gap tracking (rule R11), in R1 order.
const (
	chL2 = iota
	chBBO
	chTrades
	chCtx
	numChans
)

// chanIndex maps a feed.Kind to its channel slot (-1 if not a data channel).
func chanIndex(k feed.Kind) int {
	switch k {
	case feed.KindL2Book:
		return chL2
	case feed.KindBBO:
		return chBBO
	case feed.KindTrades:
		return chTrades
	case feed.KindAssetCtx:
		return chCtx
	}
	return -1
}

// maxGaps bounds the per-channel gap list (rule R11 housekeeping).
const maxGaps = 4

// gap is one half-open feed-gap interval [openNs, closeNs) for a single
// instrument on a single channel (rule R11). closeNs is 0 while unknown
// (treated as +inf); it is resolved at the next resubscribed event to
// resub+limit and may be lowered to the first data frame for THIS
// instrument on that channel with rx > resub (rule (a)).
type gap struct {
	openNs   int64
	resubNs  int64
	closeNs  int64 // 0 = unknown (+inf)
	hasResub bool
}

// gapList is the bounded (cap 4) list of gaps of one instrument/channel.
type gapList struct {
	gaps [maxGaps]gap
	n    int
}

// openAt records a disconnected event (its ts_ns).
func (l *gapList) openAt(ns int64) {
	if l.n == maxGaps {
		// Bounded: keep the most recent gaps, oldest evicted.
		copy(l.gaps[:], l.gaps[1:])
		l.n--
	}
	l.gaps[l.n] = gap{openNs: ns}
	l.n++
}

// resubAt records the next resubscribed event: it is the closing event of
// every gap still open (each gap references the next resubscribed in
// (ts_ns, line order); see tools/ref/README.md judgment call 4).
func (l *gapList) resubAt(ns, limitNs int64) {
	for i := 0; i < l.n; i++ {
		g := &l.gaps[i]
		if !g.hasResub {
			g.hasResub = true
			g.resubNs = ns
			g.closeNs = ns + limitNs // rule (b); trades: limit 0 = resub itself
		}
	}
}

// noteFrame lowers the close of a gap to the first data frame for this
// instrument after the resubscribed event (rule (a)). Frames arrive in
// non-decreasing rx order, so the first qualifying frame is the minimum.
func (l *gapList) noteFrame(rxNs int64) {
	for i := 0; i < l.n; i++ {
		g := &l.gaps[i]
		if g.hasResub && rxNs > g.resubNs && (g.closeNs == 0 || rxNs < g.closeNs) {
			g.closeNs = rxNs
		}
	}
}

// overlaps reports whether any gap covers second T: the window
// ((T-1)*1e9, T*1e9] intersects [open, close). An unknown close counts as
// +inf, which is causally sound because a close still unknown when T is
// emitted is always > T*1e9.
func (l *gapList) overlaps(T int64) bool {
	lo, hi := (T-1)*1e9, T*1e9
	for i := 0; i < l.n; i++ {
		g := &l.gaps[i]
		if g.closeNs != 0 && g.closeNs <= g.openNs {
			continue // degenerate empty gap never flags (judgment call 3)
		}
		if g.openNs <= hi && (g.closeNs == 0 || g.closeNs > lo) {
			return true
		}
	}
	return false
}

// dropClosed removes gaps with close <= boundNs: they can never overlap a
// second T with (T-1)*1e9 >= boundNs again.
func (l *gapList) dropClosed(boundNs int64) {
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

// tidWindow is the bounded rule-R14 dedup window over the most recent
// TidWindow accepted tids (a dropped duplicate does not refresh its
// position, judgment call 10). Structure: a preallocated ring for O(1)
// eviction order plus a fixed-size open-addressing set (linear probing,
// exact delete with backward shift) for O(1) membership — the "preallocated
// ring" and "open-addressing set" options of the task, combined. Chosen
// over a Go map because map inserts allocate overflow buckets at any load,
// which would break the hard zero-allocation requirement. The table is 2x
// the ring (load <= 0.5), so probes are short and nothing is allocated
// after newTidWindow.
type tidWindow struct {
	ring []int64 // accepted tids in accept order (eviction order)
	pos  int     // next write position in ring
	n    int     // valid entries in ring (<= len(ring))
	keys []int64 // open-addressing table
	used []bool
	mask int
}

func newTidWindow(n int) *tidWindow {
	tab := 2 * n
	if tab < 4 {
		tab = 4
	}
	for tab&(tab-1) != 0 { // round up to a power of two
		tab++
	}
	return &tidWindow{
		ring: make([]int64, n),
		keys: make([]int64, tab),
		used: make([]bool, tab),
		mask: tab - 1,
	}
}

// accept reports whether tid is new within the window; false means it is a
// duplicate in the most recent TidWindow accepted tids.
func (w *tidWindow) accept(tid int64) bool {
	if w.contains(tid) {
		return false
	}
	if w.n == len(w.ring) {
		w.remove(w.ring[w.pos]) // oldest accepted leaves the window
	} else {
		w.n++
	}
	w.ring[w.pos] = tid
	w.pos++
	if w.pos == len(w.ring) {
		w.pos = 0
	}
	w.insert(tid)
	return true
}

func (w *tidWindow) contains(tid int64) bool {
	i := int(mix64(tid)) & w.mask
	for w.used[i] {
		if w.keys[i] == tid {
			return true
		}
		i = (i + 1) & w.mask
	}
	return false
}

func (w *tidWindow) insert(tid int64) {
	i := int(mix64(tid)) & w.mask
	for w.used[i] {
		i = (i + 1) & w.mask
	}
	w.keys[i], w.used[i] = tid, true
}

// remove deletes tid with the standard backward shift so probe chains stay
// contiguous and the freed slot is really empty.
func (w *tidWindow) remove(tid int64) {
	i := int(mix64(tid)) & w.mask
	for w.used[i] && w.keys[i] != tid {
		i = (i + 1) & w.mask
	}
	if !w.used[i] {
		return
	}
	j := i
	for {
		j = (j + 1) & w.mask
		if !w.used[j] {
			break
		}
		k := int(mix64(w.keys[j])) & w.mask
		// Shift keys[j] into the gap only when its ideal slot k is not
		// cyclically inside (i, j]; otherwise moving it would break its
		// probe chain.
		if i <= j {
			if k <= j && k > i {
				continue
			}
		} else {
			if k <= j || k > i {
				continue
			}
		}
		w.keys[i], w.used[i] = w.keys[j], w.used[j]
		i = j
	}
	w.used[i] = false
}

// mix64 spreads sequential trade ids over the table (splitmix64 finalizer).
func mix64(x int64) int64 {
	u := uint64(x)
	u ^= u >> 30
	u *= 0xbf58476d1ce4e5b9
	u ^= u >> 27
	u *= 0x94d049bb133111eb
	u ^= u >> 31
	return int64(u)
}

// tradeAcc accumulates the trade_flow_1s row of one second (window
// (T-1)*1e9 < rx <= T*1e9, rule R4; arrival order, rule R14). Zeroed in
// place after each emitted row.
type tradeAcc struct {
	buyEvents, sellEvents            int64
	buyUSD, sellUSD, buyQty, sellQty float64
	maxTradeUSD, sumNotionalSq       float64
	firstMs, lastMs                  int64
	lastPx                           float64
	nPrints                          int64
	dupDropped                       int64
}

// inst is the per-instrument state: the latest frame content per kind
// (rule R6), the per-second trade bucket, the lazy R14 tid window and the
// R11 gap lists.
type inst struct {
	name string

	hasBBO               bool
	bboHasBid, bboHasAsk bool
	bboBidPx, bboBidSz   float64
	bboBidN              int32
	bboAskPx, bboAskSz   float64
	bboAskN              int32
	bboRxNs, bboTimeMs   int64

	hasL2            bool
	l2NBids, l2NAsks int
	l2Bids, l2Asks   [20]feed.Level // best first
	l2RxNs, l2TimeMs int64

	hasCtx                     bool
	ctxHave                    feed.CtxFields // presence bits of the ctx numbers
	ctxFunding, ctxPremium     float64
	ctxMark, ctxOracle         float64
	ctxOI, ctxDayNtl           float64
	ctxImpactBid, ctxImpactAsk float64
	ctxHasImpact               bool
	ctxRxNs                    int64

	fund FundingSchedule // resolved R10 schedule (IntervalS 0: none)
	fs   *fundState      // funding_settlement state (nil without EnableFunding or schedule)

	trade    tradeAcc
	micro    microState // R21-R26 (micro.go)
	tids     *tidWindow
	gaps     [numChans]gapList
	overflow overflowList
}

// ResetInstrument discards one instrument's data, gaps and accumulators, while
// retaining its schedule, open funding clock, overflow evidence and allocated
// storage. The grid watermark and every other instrument are unchanged.
func (g *Grid) ResetInstrument(instrument string) {
	i, ok := g.index[instrument]
	if !ok {
		return
	}
	in := &g.insts[i]
	if w := in.tids; w != nil {
		clear(w.ring)
		clear(w.keys)
		clear(w.used)
		w.pos, w.n = 0, 0
	}
	if fs := in.fs; fs != nil {
		*fs = fundState{I: fs.I, curS: fs.curS, open: fs.open, overflow: fs.overflow}
	}
	*in = inst{name: in.name, fund: in.fund, fs: in.fs, tids: in.tids, overflow: in.overflow}
}

// fillMarketState builds the market_state_1s row of in at boundary T
// (docs/DATA_POINTS.md 2.1, rules R5-R13). By construction of the emission
// trigger the state only contains frames with rx <= T*1e9.
func (g *Grid) fillMarketState(r *rowsink.MarketState, in *inst, T int64) {
	*r = rowsink.MarketState{}
	r.TsS = T
	r.Instrument = in.name
	r.MidPxHigh, r.MidPxLow, r.BboUpdates = in.micro.midHigh, in.micro.midLow, in.micro.bboUpdates // R21
	endNs := T * 1e9

	// R5 ages (floor((T*1e9 - rx)/1e6)) and R6 raw columns: raw carries the
	// last known frame content even when stale; null only if the kind was
	// never received or that side was absent in the last frame.
	var bboAgeMs, l2AgeMs, ctxAgeMs int64
	if in.hasBBO {
		bboAgeMs = (endNs - in.bboRxNs) / 1e6
		r.BboAgeMs = rowsink.SomeI(bboAgeMs)
		if in.bboTimeMs != 0 {
			r.LastBboMs = rowsink.SomeI(in.bboTimeMs) // venue time; 0 = frame carried none
		}
		if in.bboHasBid {
			r.BidPx = rowsink.Some(in.bboBidPx)
			r.BidSz = rowsink.Some(in.bboBidSz)
			r.BidN = rowsink.SomeI(int64(in.bboBidN))
		}
		if in.bboHasAsk {
			r.AskPx = rowsink.Some(in.bboAskPx)
			r.AskSz = rowsink.Some(in.bboAskSz)
			r.AskN = rowsink.SomeI(int64(in.bboAskN))
		}
	}
	nBids, nAsks := in.l2NBids, in.l2NAsks
	if nBids > 20 {
		nBids = 20 // decoder contract: ErrTooManyLevels above 20
	}
	if nAsks > 20 {
		nAsks = 20
	}
	var bestBid, bestAsk float64
	if in.hasL2 {
		l2AgeMs = (endNs - in.l2RxNs) / 1e6
		r.L2AgeMs = rowsink.SomeI(l2AgeMs)
		if in.l2TimeMs != 0 {
			r.LastL2Ms = rowsink.SomeI(in.l2TimeMs)
		}
		r.L2LevelsBid = rowsink.SomeI(int64(nBids))
		r.L2LevelsAsk = rowsink.SomeI(int64(nAsks))
		if nBids > 0 {
			bestBid = in.l2Bids[0].Px
		}
		if nAsks > 0 {
			bestAsk = in.l2Asks[0].Px
		}
	}
	if in.hasCtx {
		ctxAgeMs = (endNs - in.ctxRxNs) / 1e6
		r.CtxAgeMs = rowsink.SomeI(ctxAgeMs)
		// last_ctx_ms stays null: activeAssetCtx frames carry no venue
		// `time` and feed.AssetCtx has no such field (ruling 2026-10-30).
		// Every ctx-derived column is null when its Have bit is clear:
		// a missing number reads as NULL, never as 0.
		h := in.ctxHave
		if h&feed.CtxMarkPx != 0 {
			r.MarkPx = rowsink.Some(in.ctxMark)
		}
		if h&feed.CtxOraclePx != 0 {
			r.OraclePx = rowsink.Some(in.ctxOracle)
		}
		if h&feed.CtxFunding != 0 {
			r.FundingRate = rowsink.Some(in.ctxFunding)
		}
		if h&feed.CtxPremium != 0 {
			r.Premium = rowsink.Some(in.ctxPremium)
		}
		if h&feed.CtxOpenInterest != 0 {
			r.OpenInterest = rowsink.Some(in.ctxOI)
			if h&feed.CtxMarkPx != 0 {
				r.OpenInterestUSD = rowsink.Some(in.ctxOI * in.ctxMark) // single product, feeds no sum
			}
		}
		if in.ctxHasImpact {
			r.ImpactBidPx = rowsink.Some(in.ctxImpactBid)
			r.ImpactAskPx = rowsink.Some(in.ctxImpactAsk)
		}
		if h&feed.CtxDayNtlVlm != 0 {
			r.DayNtlVlmUSD = rowsink.Some(in.ctxDayNtl)
		}
	}

	// R7 validity. Age exactly at the limit is valid.
	bboValid := false
	if in.hasBBO && in.bboHasBid && in.bboHasAsk {
		bboValid = in.bboBidPx < in.bboAskPx && in.bboBidSz > 0 && in.bboAskSz > 0 &&
			bboAgeMs <= g.cfg.MaxBBOStalenessMs
	}
	bookValid := false
	if in.hasL2 {
		bookValid = nBids >= 1 && nAsks >= 1 && bestBid < bestAsk &&
			l2AgeMs <= g.cfg.MaxL2StalenessMs
	}
	ctxValid := in.hasCtx && ctxAgeMs <= g.cfg.MaxCtxStalenessMs

	// R8 mid with fallback to the l2 best.
	var mid float64
	midOk := false
	switch {
	case bboValid:
		mid = (in.bboBidPx + in.bboAskPx) / 2
		midOk = true
		r.MidSrc = rowsink.MidBBO
	case bookValid:
		mid = (bestBid + bestAsk) / 2
		midOk = true
		r.MidSrc = rowsink.MidL2
	default:
		r.MidSrc = rowsink.MidNone
	}
	if midOk {
		r.MidPx = rowsink.Some(mid)
	}

	// R9 derived bbo columns, only when bbo_valid. Operation order is the
	// frozen oracle order (tools/ref/README.md "Floats").
	if bboValid {
		bid, ask := in.bboBidPx, in.bboAskPx
		bs, asz := in.bboBidSz, in.bboAskSz
		r.SpreadBps = rowsink.Some(1e4 * (ask - bid) / mid)
		// FMA barrier on both products feeding the numerator sum.
		micro := (float64(ask*bs) + float64(bid*asz)) / (bs + asz)
		r.MicroPx = rowsink.Some(micro)
		r.MicroMinusMidBps = rowsink.Some(1e4 * (micro - mid) / mid)
		r.ImbalanceL1 = rowsink.Some((bs - asz) / (bs + asz))
	}

	// R9 depth from the latest l2 frame whenever an l2 frame exists and
	// mid_px is not null (book validity not required; sums accumulated
	// best to worst level).
	if in.hasL2 && midOk {
		// Thresholds in the R9 operation order, at run time from the
		// layout slice (never constant-folded).
		for b, bps := range g.cfg.Layout.DepthBandsBps {
			e := bps / 10000.0
			tb := 1.0 - e
			ta := 1.0 + e
			thrBid := mid * tb
			thrAsk := mid * ta
			var sb, sa float64
			for k := 0; k < nBids; k++ {
				lv := &in.l2Bids[k]
				if lv.Px >= thrBid {
					sb += float64(lv.Px * lv.Sz) // FMA barrier
				}
			}
			for k := 0; k < nAsks; k++ {
				lv := &in.l2Asks[k]
				if lv.Px <= thrAsk {
					sa += float64(lv.Px * lv.Sz) // FMA barrier
				}
			}
			r.DepthBid[b] = rowsink.Some(sb)
			r.DepthAsk[b] = rowsink.Some(sa)
		}
		if nBids > 0 {
			r.L2ReachBid = rowsink.Some(1e4 * (mid - in.l2Bids[nBids-1].Px) / mid)
		}
		if nAsks > 0 {
			r.L2ReachAsk = rowsink.Some(1e4 * (in.l2Asks[nAsks-1].Px - mid) / mid)
		}
	}

	// mark_oracle_bps needs mark_px and oracle_px present (Have bits);
	// mid_oracle_bps needs oracle_px present and a mid. An oracle of 0
	// cannot be divided (the Python oracle raises there), so those stay
	// null instead of emitting a fabricated infinity.
	if in.hasCtx && in.ctxOracle != 0 {
		if in.ctxHave&feed.CtxMarkPx != 0 && in.ctxHave&feed.CtxOraclePx != 0 {
			r.MarkOracleBps = rowsink.Some(1e4 * (in.ctxMark/in.ctxOracle - 1))
		}
		if midOk && in.ctxHave&feed.CtxOraclePx != 0 {
			r.MidOracleBps = rowsink.Some(1e4 * (mid/in.ctxOracle - 1))
		}
	}

	// R10 settlement clock from the instrument's configured schedule (I, A):
	// funding_interval_s = I, time_to_settle_s = I - posmod(T - A, I), range
	// 1..I (wraps to I exactly at a settlement second). No schedule: both
	// columns null.
	if I := in.fund.IntervalS; I > 0 {
		m := (T - in.fund.AnchorS) % I // Go % can be negative: fix to posmod
		if m < 0 {
			m += I
		}
		r.FundingIntervalS = rowsink.SomeI(I)
		r.TimeToSettleS = rowsink.SomeI(I - m)
	}

	// R11 gap flags, evaluated causally at emission time.
	feedGap := false
	for c := 0; c < numChans; c++ {
		if in.gaps[c].overlaps(T) {
			feedGap = true
			break
		}
	}
	tradeFeedValid := !in.gaps[chTrades].overlaps(T)

	// R12 quality bits.
	var q uint16
	if !in.hasBBO {
		q |= rowsink.QBBONever
	} else {
		if bboAgeMs > g.cfg.MaxBBOStalenessMs {
			q |= rowsink.QBBOStale
		}
		if !in.bboHasBid || !in.bboHasAsk {
			q |= rowsink.QBBOOneSided
		} else if in.bboBidPx >= in.bboAskPx || in.bboBidSz <= 0 || in.bboAskSz <= 0 {
			q |= rowsink.QBBOCrossed // crossed, locked or zero size
		}
	}
	if !in.hasL2 {
		q |= rowsink.QL2Never
	} else {
		if l2AgeMs > g.cfg.MaxL2StalenessMs {
			q |= rowsink.QL2Stale
		}
		if nBids == 0 || nAsks == 0 || bestBid >= bestAsk {
			q |= rowsink.QL2BadBook // empty side or crossed
		}
	}
	if !in.hasCtx {
		q |= rowsink.QCtxNever
	} else if ctxAgeMs > g.cfg.MaxCtxStalenessMs {
		q |= rowsink.QCtxStale
	}
	if feedGap {
		q |= rowsink.QFeedGap
	}
	overflow := in.overflow.overlaps((T-1)*1e9, T*1e9)
	if overflow {
		q |= rowsink.QOverflow
	}
	if !tradeFeedValid {
		q |= rowsink.QTradeFeedBad
	}

	// R13 valid conjunction.
	r.BboValid = bboValid
	r.BookValid = bookValid
	r.CtxValid = ctxValid
	r.Valid = midOk && (bboValid || bookValid) && !feedGap
	r.FeedGap = feedGap
	r.EngineOverflow = overflow
	r.Quality = q
}

// fillTradeFlow builds the trade_flow_1s row of in at boundary T (2.2,
// rules R14/R15).
func (g *Grid) fillTradeFlow(r *rowsink.TradeFlow, in *inst, T int64) {
	*r = rowsink.TradeFlow{}
	g.fillMicro(r, in)
	r.TsS = T
	r.Instrument = in.name
	a := &in.trade
	r.BuyEvents, r.SellEvents = a.buyEvents, a.sellEvents
	r.BuyUSD, r.SellUSD = a.buyUSD, a.sellUSD
	r.BuyQty, r.SellQty = a.buyQty, a.sellQty
	r.MaxTradeUSD, r.SumNotionalSq = a.maxTradeUSD, a.sumNotionalSq
	r.DupDropped = a.dupDropped
	if a.nPrints > 0 {
		// Judgment call 8: additionally null when the denominator is zero
		// despite accepted prints (zero-size prints).
		if q := a.buyQty + a.sellQty; q != 0 {
			r.Vwap = rowsink.Some((a.buyUSD + a.sellUSD) / q)
		}
		r.FirstTradeMs = rowsink.SomeI(a.firstMs)
		r.LastTradeMs = rowsink.SomeI(a.lastMs)
		r.LastTradePx = rowsink.Some(a.lastPx)
		if u := a.buyUSD + a.sellUSD; u != 0 {
			r.FlowImbalance = rowsink.Some((a.buyUSD - a.sellUSD) / u)
		}
	}
	r.TradeFeedValid = !in.gaps[chTrades].overlaps(T)
}
