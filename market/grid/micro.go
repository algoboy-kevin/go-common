package grid

import (
	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// Microstructure columns of schema_version 2 (docs/DATA_POINTS.md 2.1.2,
// rules R21-R26). Everything here is fixed-size per-instrument state: no
// maps, no growing slices, zero allocations on every path.
//
// Window attribution: a frame or print is applied after dataBegin has
// emitted every boundary T with T*1e9 < rx, so it lands in the window
// ((T-1)*1e9, T*1e9] of the row that emits next; emitSecond resets the
// per-window accumulators after both rows of an instrument are written.

// microAcc holds the per-window (per-second) accumulators of R22-R26.
type microAcc struct {
	sellPxMin, buyPxMax                      rowsink.F64 // R23
	sellAtBid, sellThruBid, sellUnclassified float64     // R22
	buyAtAsk, buyThruAsk, buyUnclassified    float64     // R22
	bidRemoved, bidAdded                     float64     // R25
	askRemoved, askAdded                     float64     // R25
	bidTouch, askTouch                       int64       // R25
	flags                                    int64       // R26 bits 1 and 4 (bit 2 is decided at emit)
	ladderReady                              bool        // R24 thresholds computed for this window
	sellGe, buyGe                            [rowsink.MaxLadderEdges]float64
}

// microState is the per-instrument state of R21-R26.
type microState struct {
	acc microAcc

	// R21: mid of the last qualifying bbo frame (two-sided, bid < ask),
	// the running high/low of the current window (seeded with the
	// carried-in mid at each emit) and the window's bbo frame count.
	qualMid, midHigh, midLow rowsink.F64
	bboUpdates               int64

	// R24: mid_px emitted in this instrument's previous row (null on the
	// first row of the run) and the window's lazily computed thresholds.
	prevMid         rowsink.F64
	sellThr, buyThr [rowsink.MaxLadderEdges]float64

	// R25: P exists (the previous bbo frame is still in inst.bbo* when the
	// next one arrives) and the dedup'd qty printed at P's bid / ask since.
	trackerP         bool
	trackCb, trackCa float64
}

// resetWindow closes the window after emission: accumulators to zero, the
// carried-in mid becomes the last qualifying mid at the close (R21, used
// regardless of its age) and prevMid the row's emitted mid_px (R24).
func (m *microState) resetWindow(emittedMid rowsink.F64) {
	m.acc = microAcc{}
	m.midHigh, m.midLow = m.qualMid, m.qualMid
	m.bboUpdates = 0
	m.prevMid = emittedMid
}

// clearTracker drops R25's P, cb and ca: the next bbo frame only seeds P.
func (m *microState) clearTracker() {
	m.trackerP = false
	m.trackCb, m.trackCa = 0, 0
}

// microBBO applies R21 and R25 for bbo frame n. It must run BEFORE the frame
// replaces the instrument's raw bbo fields, which then still hold P.
func (in *inst) microBBO(n *feed.BBO) {
	m := &in.micro
	m.bboUpdates++ // R21: every frame counts, whatever its content
	if n.HasBid && n.HasAsk && n.Bid.Px < n.Ask.Px {
		mid := (n.Bid.Px + n.Ask.Px) / 2
		m.qualMid = rowsink.Some(mid)
		if !m.midHigh.Ok || mid > m.midHigh.V {
			m.midHigh = m.qualMid
		}
		if !m.midLow.Ok || mid < m.midLow.V {
			m.midLow = m.qualMid
		}
	}
	if m.trackerP { // the first frame (or the first after a clear) only seeds P
		a := &m.acc
		l1Side(in.bboHasBid, n.HasBid, in.bboBidPx, n.Bid.Px, in.bboBidSz, n.Bid.Sz, m.trackCb,
			&a.bidRemoved, &a.bidAdded, &a.bidTouch, &a.flags)
		l1Side(in.bboHasAsk, n.HasAsk, in.bboAskPx, n.Ask.Px, in.bboAskSz, n.Ask.Sz, m.trackCa,
			&a.askRemoved, &a.askAdded, &a.askTouch, &a.flags)
	}
	m.trackerP = true
	m.trackCb, m.trackCa = 0, 0
}

// l1Side is R25 for one side: P has (hp, pp, ps), N has (hn, np, ns), c is
// the trade qty printed at P's price since P arrived.
func l1Side(hp, hn bool, pp, np, ps, ns, c float64, removed, added *float64, touch, flags *int64) {
	switch {
	case hp && hn && pp == np:
		t := ps - c // exactly this operation order (R25)
		r := ns - t
		if r < 0 {
			*removed += -r
		} else {
			*added += r
		}
		if c > 0 {
			*flags |= rowsink.FlowTradeAdjustedL1 // R26 bit 4
		}
	case hp != hn || hp: // presence differs, or both present at different prices
		*touch++
	}
}

// microPrint applies R22-R25 for one print that survived the R14 dedup,
// against the instrument's latest bbo frame (rx <= the print's rx by the R1
// order; a bbo at the same rx was applied first).
func (g *Grid) microPrint(in *inst, rxNs int64, t *feed.Trade) {
	m := &in.micro
	a := &m.acc
	usable := in.hasBBO && in.bboHasBid && in.bboHasAsk && in.bboBidPx < in.bboAskPx &&
		in.bboBidSz > 0 && in.bboAskSz > 0 && (rxNs-in.bboRxNs)/1e6 <= g.cfg.MaxBBOStalenessMs
	if t.Side == feed.SideBuy {
		if !a.buyPxMax.Ok || t.Px > a.buyPxMax.V {
			a.buyPxMax = rowsink.Some(t.Px)
		}
		switch {
		case usable && t.Px == in.bboAskPx:
			a.buyAtAsk += t.Sz
		case usable && t.Px > in.bboAskPx:
			a.buyThruAsk += t.Sz
		default:
			a.buyUnclassified += t.Sz
			a.flags |= rowsink.FlowUnclassified
		}
		if m.trackerP && in.bboHasAsk && t.Px == in.bboAskPx {
			m.trackCa += t.Sz
		}
	} else { // as in OnTrades: anything but 'B' is a sell
		if !a.sellPxMin.Ok || t.Px < a.sellPxMin.V {
			a.sellPxMin = rowsink.Some(t.Px)
		}
		switch {
		case usable && t.Px == in.bboBidPx:
			a.sellAtBid += t.Sz
		case usable && t.Px < in.bboBidPx:
			a.sellThruBid += t.Sz
		default:
			a.sellUnclassified += t.Sz
			a.flags |= rowsink.FlowUnclassified
		}
		if m.trackerP && in.bboHasBid && t.Px == in.bboBidPx {
			m.trackCb += t.Sz
		}
	}
	if !m.prevMid.Ok {
		return // R24: no reference, the ladder is null for this row
	}
	edges := g.cfg.Layout.LadderEdgesBps
	if !a.ladderReady {
		// Lazily, once per window with a print. Evaluated at run time from
		// the layout slice in IEEE binary64, never constant-folded (R24).
		ref := m.prevMid.V
		for i, edge := range edges {
			e := edge / 10000.0
			lo := 1.0 - e
			hi := 1.0 + e
			m.sellThr[i] = ref * lo
			m.buyThr[i] = ref * hi
		}
		a.ladderReady = true
	}
	if t.Side == feed.SideBuy {
		for i := range edges {
			if t.Px >= m.buyThr[i] {
				a.buyGe[i] += t.Sz
			}
		}
	} else {
		for i := range edges {
			if t.Px <= m.sellThr[i] {
				a.sellGe[i] += t.Sz
			}
		}
	}
}

// fillMicro writes the R21-R26 trade_flow columns of the window into r.
func (g *Grid) fillMicro(r *rowsink.TradeFlow, in *inst) {
	m := &in.micro
	a := &m.acc
	r.SellPxMin, r.BuyPxMax = a.sellPxMin, a.buyPxMax
	r.SellQtyAtBid, r.SellQtyThruBid, r.SellQtyUnclassified = a.sellAtBid, a.sellThruBid, a.sellUnclassified
	r.BuyQtyAtAsk, r.BuyQtyThruAsk, r.BuyQtyUnclassified = a.buyAtAsk, a.buyThruAsk, a.buyUnclassified
	r.BidL1Removed, r.BidL1Added = a.bidRemoved, a.bidAdded
	r.AskL1Removed, r.AskL1Added = a.askRemoved, a.askAdded
	r.BidTouchChanges, r.AskTouchChanges = a.bidTouch, a.askTouch
	r.FlowAttribFlags = a.flags
	if in.trade.nPrints > 0 && m.bboUpdates == 0 {
		r.FlowAttribFlags |= rowsink.FlowNoBBOUpdate // R26 bit 2
	}
	if m.prevMid.Ok {
		for i := range g.cfg.Layout.LadderEdgesBps {
			r.SellQtyGe[i] = rowsink.Some(a.sellGe[i])
			r.BuyQtyGe[i] = rowsink.Some(a.buyGe[i])
		}
	}
}
