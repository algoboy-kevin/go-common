package grid

import (
	"math"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// Hand-derived unit tests for the schema_version 2 microstructure rules
// (docs/DATA_POINTS.md 2.1.2, R9 amended, R21-R26). Every expected value is
// worked out in the comments from the spec text and the inputs; none is read
// from the oracle or a golden file. Prices and sizes are dyadic (exact in
// binary64) unless a test is about rounding.

func microGrid(t *testing.T, universe ...string) (*Grid, *recSink) {
	t.Helper()
	if len(universe) == 0 {
		universe = []string{"A"}
	}
	s := &recSink{}
	g, err := New(DefaultConfig(), universe, s)
	if err != nil {
		t.Fatal(err)
	}
	return g, s
}

// q is a two-sided bbo for "A" (n = 1 on both sides).
func q(bid, bidSz, ask, askSz float64) *feed.BBO {
	return mkBBO("A", 1, true, true, feed.Level{Px: bid, Sz: bidSz, N: 1}, feed.Level{Px: ask, Sz: askSz, N: 1})
}

func sell(px, sz float64, tid int64) feed.Trade { return mkTrade("A", feed.SideSell, px, sz, 1, tid) }
func buy(px, sz float64, tid int64) feed.Trade  { return mkTrade("A", feed.SideBuy, px, sz, 1, tid) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eqNN(t *testing.T, name string, got, want float64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
}

func eqInt(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d, want %d", name, got, want)
	}
}

// TestMicroClassification (R22, R23, R14 order): dedup first, then classify;
// extremes over the deduplicated prints only.
func TestMicroClassification(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(100, 10, 101, 20))) // usable: two-sided, 100 < 101, sizes > 0
	must(t, g.OnTrades(1.5e9, []feed.Trade{
		sell(100, 1, 1),    // px == bid          -> sell_qty_at_bid 1
		sell(99.5, 2, 2),   // px <  bid          -> sell_qty_thru_bid 2
		sell(100.5, 4, 3),  // px >  bid          -> sell_qty_unclassified 4
		buy(101, 8, 4),     // px == ask          -> buy_qty_at_ask 8
		buy(102, 16, 5),    // px >  ask          -> buy_qty_thru_ask 16
		buy(100.75, 32, 6), // px <  ask          -> buy_qty_unclassified 32
		sell(1, 64, 2),     // duplicate tid 2: dropped BEFORE classification and extremes
		buy(999, 128, 5),   // duplicate tid 5: dropped
	}))
	g.Flush(2e9)
	r := s.tfAt(t, 2, "A")
	eqNN(t, "sell_qty_at_bid", r.SellQtyAtBid, 1)
	eqNN(t, "sell_qty_thru_bid", r.SellQtyThruBid, 2)
	eqNN(t, "sell_qty_unclassified", r.SellQtyUnclassified, 4)
	eqNN(t, "buy_qty_at_ask", r.BuyQtyAtAsk, 8)
	eqNN(t, "buy_qty_thru_ask", r.BuyQtyThruAsk, 16)
	eqNN(t, "buy_qty_unclassified", r.BuyQtyUnclassified, 32)
	eqNN(t, "sell_qty = 1+2+4", r.SellQty, 7)
	eqNN(t, "buy_qty = 8+16+32", r.BuyQty, 56)
	eqInt(t, "dup_dropped", r.DupDropped, 2)
	eqF64(t, "sell_px_min (99.5, not the dup's 1)", r.SellPxMin, 99.5)
	eqF64(t, "buy_px_max (102, not the dup's 999)", r.BuyPxMax, 102)
	// bit0: unclassified prints; bit1: prints but the bbo frame was in window 1.
	eqInt(t, "flow_attrib_flags", r.FlowAttribFlags, rowsink.FlowUnclassified|rowsink.FlowNoBBOUpdate)
	// Window 1 had the bbo frame and no prints: extremes null, flags 0.
	r1 := s.tfAt(t, 1, "A")
	nullF64(t, "sell_px_min@1", r1.SellPxMin)
	nullF64(t, "buy_px_max@1", r1.BuyPxMax)
	eqInt(t, "flags@1", r1.FlowAttribFlags, 0)
}

// TestMicroClassificationStaleness (R22 with R7): age = floor((print_rx -
// bbo_rx)/1e6) <= 2000 ms is usable; 2001 ms is not.
func TestMicroClassificationStaleness(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(100, 10, 101, 20)))
	// 3e9+999999 - 1e9 = 2000.999999 ms -> floor 2000 -> usable, at bid.
	must(t, g.OnTrades(3e9+999999, []feed.Trade{sell(100, 1, 1)}))
	// 3.001e9 - 1e9 = 2001 ms -> stale -> unclassified.
	must(t, g.OnTrades(3.001e9, []feed.Trade{buy(101, 2, 2)}))
	g.Flush(4e9)
	r := s.tfAt(t, 4, "A")
	eqNN(t, "sell at bid (age 2000)", r.SellQtyAtBid, 1)
	eqNN(t, "buy unclassified (age 2001)", r.BuyQtyUnclassified, 2)
	eqNN(t, "buy at ask", r.BuyQtyAtAsk, 0)
	eqInt(t, "flags", r.FlowAttribFlags, rowsink.FlowUnclassified|rowsink.FlowNoBBOUpdate)
}

// TestMicroClassificationUnusable (R22): one-sided, locked/crossed, zero size
// or no bbo at all -> unclassified even at the touch price.
func TestMicroClassificationUnusable(t *testing.T) {
	oneSided := q(100, 10, 101, 20)
	oneSided.HasAsk = false
	cases := map[string]*feed.BBO{
		"one-sided":   oneSided,
		"locked":      q(100, 10, 100, 20),
		"crossed":     q(100, 10, 99, 20),
		"zero bid sz": q(100, 0, 101, 20),
		"zero ask sz": q(100, 10, 101, 0),
		"none":        nil,
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			g, s := microGrid(t)
			if b != nil {
				must(t, g.OnBBO(1e9, b))
			} else {
				must(t, g.OnL2Book(1e9, goodL2("A", 1))) // start the grid without a bbo
			}
			must(t, g.OnTrades(1.5e9, []feed.Trade{sell(100, 3, 1)}))
			g.Flush(2e9)
			r := s.tfAt(t, 2, "A")
			eqNN(t, "sell unclassified", r.SellQtyUnclassified, 3)
			eqNN(t, "sell at bid", r.SellQtyAtBid, 0)
			eqF64(t, "sell_px_min (classified or not)", r.SellPxMin, 100)
			if r.FlowAttribFlags&rowsink.FlowUnclassified == 0 {
				t.Fatalf("flags %d lack bit 1", r.FlowAttribFlags)
			}
		})
	}
}

// TestMicroL1Residual (R25): t = P.sz - cb; r = N.sz - t; trades larger than
// the visible size; dedup before cb; the first frame only seeds.
func TestMicroL1Residual(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(100, 10, 101, 20))) // first frame of the run: seeds P only
	must(t, g.OnTrades(1.25e9, []feed.Trade{
		sell(100, 3, 1),  // at P.bid -> cb = 3
		buy(101, 25, 2),  // at P.ask, larger than the visible 20 -> ca = 25
		sell(99, 1, 3),   // not at P.bid -> cb unchanged
		sell(100, 50, 1), // duplicate tid 1: dropped before cb
	}))
	// bid: t = 10-3 = 7, r = 4-7 = -3 -> removed 3.
	// ask: t = 20-25 = -5, r = 2-(-5) = 7 -> added 7 (no clamping).
	must(t, g.OnBBO(1.5e9, q(100, 4, 101, 2)))
	// No prints since: bid t = 4, r = 6-4 = 2 -> added 2; ask t = 2, r = 0 -> added 0.
	must(t, g.OnBBO(1.75e9, q(100, 6, 101, 2)))
	g.Flush(2e9)
	r1 := s.tfAt(t, 1, "A")
	eqNN(t, "seed bid removed", r1.BidL1Removed, 0)
	eqNN(t, "seed bid added", r1.BidL1Added, 0)
	eqInt(t, "seed bid touch", r1.BidTouchChanges, 0)
	eqInt(t, "seed ask touch", r1.AskTouchChanges, 0)
	r := s.tfAt(t, 2, "A")
	eqNN(t, "bid_l1_removed_qty", r.BidL1Removed, 3)
	eqNN(t, "bid_l1_added_qty", r.BidL1Added, 2)
	eqNN(t, "ask_l1_removed_qty", r.AskL1Removed, 0)
	eqNN(t, "ask_l1_added_qty", r.AskL1Added, 7)
	eqInt(t, "bid_touch_changes", r.BidTouchChanges, 0)
	eqInt(t, "ask_touch_changes", r.AskTouchChanges, 0)
	// Prints classified against the seed (usable, 250 ms): at bid 3, at ask 25, thru 1.
	eqNN(t, "sell at bid", r.SellQtyAtBid, 3)
	eqNN(t, "sell thru", r.SellQtyThruBid, 1)
	eqNN(t, "buy at ask", r.BuyQtyAtAsk, 25)
	// bit 4 from the frame at 1.5e9 (cb, ca > 0 at unchanged prices); two
	// bbo updates, no unclassified print.
	eqInt(t, "flow_attrib_flags", r.FlowAttribFlags, rowsink.FlowTradeAdjustedL1)
	eqInt(t, "bbo_updates", s.msAt(t, 2, "A").BboUpdates, 2)
}

// TestMicroTouchChanges (R25): price moves and presence changes count, both
// sides absent is unchanged, and a touch change adds nothing to
// removed/added (nor flag 4, even with cb > 0).
func TestMicroTouchChanges(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(100, 10, 101, 20)))                 // seed
	must(t, g.OnBBO(1.2e9, q(99.5, 10, 101, 20)))              // bid moved: touch 1; ask r = 20-20 = 0 -> added 0
	must(t, g.OnTrades(1.3e9, []feed.Trade{sell(99.5, 2, 1)})) // at P.bid 99.5 -> cb = 2
	noBid := q(0, 0, 101, 15)
	noBid.HasBid = false
	must(t, g.OnBBO(1.4e9, noBid)) // bid presence lost: touch 2 (cb ignored); ask r = 15-20 = -5 -> removed 5
	none := q(0, 0, 0, 0)
	none.HasBid, none.HasAsk = false, false
	must(t, g.OnBBO(1.6e9, none)) // bid absent both: unchanged; ask presence lost: ask touch 1
	bidOnly := q(100, 1, 0, 0)
	bidOnly.HasAsk = false
	must(t, g.OnBBO(1.8e9, bidOnly)) // bid presence back: touch 3; ask absent both: unchanged
	g.Flush(2e9)
	r := s.tfAt(t, 2, "A")
	eqInt(t, "bid_touch_changes", r.BidTouchChanges, 3)
	eqInt(t, "ask_touch_changes", r.AskTouchChanges, 1)
	eqNN(t, "bid removed", r.BidL1Removed, 0)
	eqNN(t, "bid added", r.BidL1Added, 0)
	eqNN(t, "ask removed", r.AskL1Removed, 5)
	eqNN(t, "ask added", r.AskL1Added, 0)
	eqNN(t, "sell at bid (bbo at 1.2e9 usable)", r.SellQtyAtBid, 2)
	eqInt(t, "flags (touch change with cb > 0 is not bit 4)", r.FlowAttribFlags, 0)
	eqInt(t, "bbo_updates", s.msAt(t, 2, "A").BboUpdates, 4)
}

// TestMicroSentinelClearsTracker (R25): disconnected / resubscribed on bbo or
// trades clears P, cb and ca; l2Book/activeAssetCtx and other events do not.
// A data frame at the event's ts is processed before the reset (the caller
// orders it first; the grid applies calls in order).
func TestMicroSentinelClearsTracker(t *testing.T) {
	run := func(t *testing.T, ch feed.Kind, ev MetaEvent, metaAt int64, bboAtMeta bool) *rowsink.TradeFlow {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 10, 101, 20)))
		must(t, g.OnTrades(1.2e9, []feed.Trade{sell(100, 3, 1)})) // cb = 3
		if bboAtMeta {
			must(t, g.OnBBO(metaAt, q(100, 4, 101, 20))) // same rx as the event: before the reset
		}
		must(t, g.OnMeta(metaAt, ch, ev))
		must(t, g.OnBBO(1.8e9, q(100, 4, 101, 20)))
		must(t, g.OnBBO(1.9e9, q(100, 5, 101, 20)))
		g.Flush(2e9)
		return s.tfAt(t, 2, "A")
	}
	for _, ch := range []feed.Kind{feed.KindBBO, feed.KindTrades} {
		for _, ev := range []MetaEvent{MetaDisconnected, MetaResubscribed} {
			r := run(t, ch, ev, 1.5e9, false)
			// Cleared: 1.8e9 only seeds; 1.9e9 vs 1.8e9: bid r = 5-4 = 1 -> added 1.
			if r.BidL1Removed != 0 || r.BidL1Added != 1 || r.FlowAttribFlags != 0 {
				t.Fatalf("ch=%v ev=%v: removed %v added %v flags %d, want 0/1/0", ch, ev, r.BidL1Removed, r.BidL1Added, r.FlowAttribFlags)
			}
			// Tie: the frame at 1.5e9 runs first against P(1e9): t = 10-3 = 7,
			// r = 4-7 = -3 -> removed 3, bit 4. Then reset; 1.8e9 only seeds
			// (no residual 4-4); 1.9e9 adds 1.
			r = run(t, ch, ev, 1.5e9, true)
			if r.BidL1Removed != 3 || r.BidL1Added != 1 || r.FlowAttribFlags != rowsink.FlowTradeAdjustedL1 {
				t.Fatalf("tie ch=%v ev=%v: removed %v added %v flags %d, want 3/1/4", ch, ev, r.BidL1Removed, r.BidL1Added, r.FlowAttribFlags)
			}
		}
	}
	// Not cleared: 1.8e9 vs P(1e9): t = 7, r = 4-7 = -3 -> removed 3, bit 4;
	// 1.9e9: added 1.
	for _, c := range []struct {
		ch feed.Kind
		ev MetaEvent
	}{{feed.KindL2Book, MetaDisconnected}, {feed.KindAssetCtx, MetaResubscribed}, {feed.KindBBO, MetaOther}} {
		r := run(t, c.ch, c.ev, 1.5e9, false)
		if r.BidL1Removed != 3 || r.BidL1Added != 1 || r.FlowAttribFlags != rowsink.FlowTradeAdjustedL1 {
			t.Fatalf("ch=%v ev=%v must not clear: removed %v added %v flags %d", c.ch, c.ev, r.BidL1Removed, r.BidL1Added, r.FlowAttribFlags)
		}
	}
}

// TestMicroMidRange (R21): carried-in mid, unchanged second, one-sided and
// crossed frames (counted, not qualifying), null when never qualified.
func TestMicroMidRange(t *testing.T) {
	g, s := microGrid(t, "A", "B")
	must(t, g.OnBBO(1e9, q(99, 0, 101, 0))) // qualifies (sizes not required): mid 100
	bad := mkBBO("B", 1, true, true, feed.Level{Px: 5, Sz: 1}, feed.Level{Px: 5, Sz: 1})
	must(t, g.OnBBO(1e9, bad))                 // B: locked, never qualifies
	must(t, g.OnBBO(1.5e9, q(101, 1, 103, 1))) // mid 102
	oneSided := q(0, 0, 103, 1)
	oneSided.HasBid = false
	must(t, g.OnBBO(2.5e9, oneSided))
	must(t, g.OnBBO(2.6e9, q(105, 1, 104, 1)))   // crossed
	must(t, g.OnBBO(2.7e9, q(104, 1, 104, 1)))   // locked
	must(t, g.OnBBO(4.5e9, q(98, 1, 100, 1)))    // mid 99
	must(t, g.OnBBO(4.7e9, q(101, 1, 101.5, 1))) // mid 101.25
	g.Flush(6e9)
	type want struct {
		T         int64
		high, low float64
		updates   int64
	}
	for _, w := range []want{
		{1, 100, 100, 1},       // the frame's own mid
		{2, 102, 100, 1},       // carried-in 100 is the low, new 102 the high
		{3, 102, 102, 3},       // three non-qualifying frames: only the carried-in 102
		{4, 102, 102, 0},       // unchanged second: carried-in mid regardless of its age
		{5, 102, 99, 2},        // carried 102 high, 99 low, 101.25 in between
		{6, 101.25, 101.25, 0}, // carried-in = the LAST qualifying mid, not the high
	} {
		r := s.msAt(t, w.T, "A")
		eqF64(t, "mid_px_high", r.MidPxHigh, w.high)
		eqF64(t, "mid_px_low", r.MidPxLow, w.low)
		eqInt(t, "bbo_updates", r.BboUpdates, w.updates)
	}
	rb := s.msAt(t, 1, "B")
	nullF64(t, "B high", rb.MidPxHigh)
	nullF64(t, "B low", rb.MidPxLow)
	eqInt(t, "B bbo_updates (non-qualifying counted)", rb.BboUpdates, 1)
	eqInt(t, "B bbo_updates@2", s.msAt(t, 2, "B").BboUpdates, 0)
}

// TestMicroLadder (R24): ref = previous row's mid_px = 10 (bbo 9/11).
// Hand derivation of the 1000 bps thresholds: e = 0.1, 1.0-e is the double
// 0.900000000000000022..., times 10 = 9.00000000000000022 which rounds to
// exactly 9.0 (ulp at 9 is 1.78e-15); 1.0+e = 1.100000000000000088817...,
// times 10 = 11 + 2^-50 exactly, a tie between 11 and 11+2^-49 that rounds to
// the even 11.0. So both thresholds are exact and px == thr must count.
func TestMicroLadder(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(9, 1, 11, 1)))
	must(t, g.OnTrades(1e9, []feed.Trade{sell(1, 1, 100)})) // window 1: first row, ref null
	g.Flush(1e9)
	up, down := math.Inf(1), math.Inf(-1)
	must(t, g.OnTrades(1.5e9, []feed.Trade{
		sell(9, 1, 1),                     // == thr(1000): counts at every edge
		sell(math.Nextafter(9, up), 2, 2), // one ulp inside of 9: not 1000, all others
		sell(math.Nextafter(9, down), 4, 3),
		sell(9.9, 8, 4), // <= 10*(1-64e-4) = 9.936, > 10*(1-128e-4) = 9.872: edges 0.5..64
		sell(10, 16, 5), // above 10*(1-0.5e-4) = 9.9995: no edge
		buy(11, 1, 6),
		buy(math.Nextafter(11, down), 2, 7),
		buy(math.Nextafter(11, up), 4, 8),
		buy(10.1, 8, 9), // >= 10.064 (64 bps), < 10.128 (128 bps)
		buy(10, 16, 10),
		sell(9, 32, 1), // duplicate tid: not on the ladder
	}))
	g.Flush(2e9)
	for _, v := range s.tfAt(t, 1, "A").SellQtyGe[:12] {
		nullF64(t, "ladder@1 (first row of the run)", v)
	}
	// Edges 0.5..64 (idx 0-7): 1+2+4+8 = 15; 128..512 (idx 8-10): 1+2+4 = 7;
	// 1000 (idx 11): 1+4 = 5 (the one-ulp-inside print misses it).
	want := []float64{15, 15, 15, 15, 15, 15, 15, 15, 7, 7, 7, 5}
	r := s.tfAt(t, 2, "A")
	for i, w := range want {
		eqF64(t, "sell_qty_ge", r.SellQtyGe[i], w)
		eqF64(t, "buy_qty_ge", r.BuyQtyGe[i], w)
	}
	for i := 12; i < rowsink.MaxLadderEdges; i++ {
		nullF64(t, "inactive edge", r.SellQtyGe[i])
	}
	// Window 3: no prints, ref 10 known -> zeros, not null.
	g.Flush(3e9)
	eqF64(t, "ladder@3 with no prints", s.tfAt(t, 3, "A").SellQtyGe[0], 0)
	// Thresholds are per window: mid becomes 20 (bbo 19/21) in window 4, so
	// window 5 uses ref 20: 20*0.9 = 18.0000000000000004 -> 18.0 exactly.
	must(t, g.OnBBO(3.5e9, mkBBO("A", 1, true, true, feed.Level{Px: 19, Sz: 1}, feed.Level{Px: 21, Sz: 1})))
	g.Flush(4e9)
	must(t, g.OnTrades(4.5e9, []feed.Trade{sell(18, 1, 20), sell(9, 2, 21)}))
	g.Flush(5e9)
	eqF64(t, "ref 20: 18 counts at 1000", s.tfAt(t, 5, "A").SellQtyGe[11], 3)
	eqF64(t, "ref 20: both count at 0.5", s.tfAt(t, 5, "A").SellQtyGe[0], 3)
}

// TestMicroLadderNullReference (R24): a null previous mid nulls the whole
// ladder even when the current quote is fine.
func TestMicroLadderNullReference(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnL2Book(1e9, mkL2("A", 1, []feed.Level{{Px: 9, Sz: 1}}, []feed.Level{{Px: 8, Sz: 1}}))) // crossed: mid null
	g.Flush(1e9)
	must(t, g.OnBBO(1.5e9, q(9, 1, 11, 1)))
	must(t, g.OnTrades(1.5e9, []feed.Trade{sell(1, 1, 1)}))
	g.Flush(2e9)
	nullF64(t, "mid@1", s.msAt(t, 1, "A").MidPx)
	r := s.tfAt(t, 2, "A")
	for i := 0; i < 12; i++ {
		nullF64(t, "sell ladder with null ref", r.SellQtyGe[i])
		nullF64(t, "buy ladder with null ref", r.BuyQtyGe[i])
	}
	eqNN(t, "the print still counts in sell_qty", r.SellQty, 1)
	must(t, g.OnTrades(2.5e9, []feed.Trade{sell(1, 2, 2)}))
	g.Flush(3e9)
	eqF64(t, "ref 10 from row 2", s.tfAt(t, 3, "A").SellQtyGe[11], 2)
}

// TestMicroReachAndBands (R9): reach from mid_px (here the l2 fallback, the
// bbo is stale), incomplete bands are lower bounds and never null.
func TestMicroReachAndBands(t *testing.T) {
	g, s := microGrid(t)
	must(t, g.OnBBO(1e9, q(99, 1, 101, 1))) // bbo mid 100, stale at T=4 (3000 ms)
	must(t, g.OnL2Book(4e9, mkL2("A", 1,
		[]feed.Level{{Px: 199, Sz: 2}, {Px: 198, Sz: 3}},
		[]feed.Level{{Px: 201, Sz: 4}, {Px: 202, Sz: 5}})))
	g.Flush(4e9)
	r := s.msAt(t, 4, "A")
	eqF64(t, "mid_px (l2 fallback)", r.MidPx, 200)
	// 1e4*(200-198)/200 = 100; 1e4*(202-200)/200 = 100.
	eqF64(t, "l2_reach_bid_bps", r.L2ReachBid, 100)
	eqF64(t, "l2_reach_ask_bps", r.L2ReachAsk, 100)
	// 1000 bps band: thr 180 / 220, every level: 199*2+198*3 = 992 and
	// 201*4+202*5 = 1814; incomplete (reach 100 < 1000) but not null.
	eqF64(t, "depth_bid_1000 (incomplete)", r.DepthBid[9], 992)
	eqF64(t, "depth_ask_1000 (incomplete)", r.DepthAsk[9], 1814)
	// 20 bps: thr 199.6 / 200.4, no level inside: 0, not null.
	eqF64(t, "depth_bid_20", r.DepthBid[4], 0)
	eqF64(t, "depth_ask_20", r.DepthAsk[4], 0)

	// Empty ask side: reach null on that side only, its sums 0.
	g2, s2 := microGrid(t)
	must(t, g2.OnBBO(1e9, q(99, 1, 101, 1)))
	must(t, g2.OnL2Book(1e9, mkL2("A", 1, []feed.Level{{Px: 99, Sz: 1}, {Px: 98, Sz: 1}}, nil)))
	g2.Flush(1e9)
	r = s2.msAt(t, 1, "A")
	eqF64(t, "bid reach (bbo mid 100)", r.L2ReachBid, 200) // 1e4*(100-98)/100
	nullF64(t, "ask reach (no levels)", r.L2ReachAsk)
	eqF64(t, "ask band with no levels", r.DepthAsk[9], 0)
	// Without mid: depth and reach null.
	g3, s3 := microGrid(t)
	must(t, g3.OnL2Book(1e9, mkL2("A", 1, []feed.Level{{Px: 99, Sz: 1}}, nil)))
	g3.Flush(1e9)
	r = s3.msAt(t, 1, "A")
	nullF64(t, "reach without mid", r.L2ReachBid)
	nullF64(t, "depth without mid", r.DepthBid[0])
}

// TestMicroFlags (R26): every bit alone and combined.
func TestMicroFlags(t *testing.T) {
	t.Run("bit 1 only: unclassified print with a bbo update", func(t *testing.T) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1.5e9, q(100, 1, 99, 1))) // crossed, counted as an update
		must(t, g.OnTrades(1.6e9, []feed.Trade{sell(100, 1, 1)}))
		g.Flush(2e9)
		eqInt(t, "flags", s.tfAt(t, 2, "A").FlowAttribFlags, rowsink.FlowUnclassified)
	})
	t.Run("bit 2 only: classified prints, no update in the second", func(t *testing.T) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 1, 101, 1)))
		must(t, g.OnTrades(1.5e9, []feed.Trade{sell(100, 1, 1)}))
		g.Flush(2e9)
		eqInt(t, "flags", s.tfAt(t, 2, "A").FlowAttribFlags, rowsink.FlowNoBBOUpdate)
	})
	t.Run("only duplicates: no deduplicated print, no bit 2", func(t *testing.T) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 1, 101, 1)))
		must(t, g.OnTrades(1e9, []feed.Trade{sell(100, 1, 1)}))
		must(t, g.OnTrades(1.5e9, []feed.Trade{sell(100, 1, 1)}))
		g.Flush(2e9)
		r := s.tfAt(t, 2, "A")
		eqInt(t, "dup_dropped", r.DupDropped, 1)
		eqInt(t, "flags", r.FlowAttribFlags, 0)
	})
	t.Run("bits 1 and 4: unusable quote, cb > 0 at an unchanged bid", func(t *testing.T) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 10, 101, 0)))                 // zero ask size: unusable, P has a bid
		must(t, g.OnTrades(1.2e9, []feed.Trade{sell(100, 3, 1)})) // unclassified, cb = 3
		must(t, g.OnBBO(1.4e9, q(100, 7, 101, 0)))                // t = 10-3 = 7, r = 7-7 = 0 -> added 0
		g.Flush(2e9)
		r := s.tfAt(t, 2, "A")
		eqInt(t, "flags", r.FlowAttribFlags, rowsink.FlowUnclassified|rowsink.FlowTradeAdjustedL1)
		eqNN(t, "added", r.BidL1Added, 0)
		eqNN(t, "removed", r.BidL1Removed, 0)
	})
	t.Run("no prints, no updates: 0", func(t *testing.T) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 1, 101, 1)))
		g.Flush(3e9)
		eqInt(t, "flags", s.tfAt(t, 3, "A").FlowAttribFlags, 0)
	})
}

// TestMicroOverflowAndReset: MarkOverflow clears the R25 tracker (dropped
// frames break P's continuity) and keeps the window's accumulators, like the
// trade bucket; ResetInstrument discards everything of the instrument.
func TestMicroOverflowAndReset(t *testing.T) {
	setup := func(t *testing.T) (*Grid, *recSink) {
		g, s := microGrid(t)
		must(t, g.OnBBO(1e9, q(100, 10, 101, 20))) // mid 100.5 in row 1
		g.Flush(1e9)
		must(t, g.OnTrades(1.2e9, []feed.Trade{sell(100, 3, 1)})) // cb = 3, at bid
		return g, s
	}
	t.Run("MarkOverflow", func(t *testing.T) {
		g, s := setup(t)
		g.MarkOverflow("A", 1.1e9, 1.3e9)
		must(t, g.OnBBO(1.5e9, q(100, 4, 103, 20))) // only seeds after the clear (mid 101.5)
		must(t, g.OnBBO(1.8e9, q(100, 5, 103, 20))) // vs 1.5e9: bid added 1
		g.Flush(2e9)
		r := s.tfAt(t, 2, "A")
		eqNN(t, "removed", r.BidL1Removed, 0)
		eqNN(t, "added", r.BidL1Added, 1)
		eqInt(t, "flags (no bit 4)", r.FlowAttribFlags, 0)
		eqNN(t, "received print kept", r.SellQtyAtBid, 3)
		// 3 <= 100.5*(1-0.1) = 90.45? no: 100 > 90.45; 100 <= 100.5*(1-0.5e-4) ~ 100.495: yes.
		eqF64(t, "ladder ref kept", r.SellQtyGe[0], 3)
		m := s.msAt(t, 2, "A")
		eqF64(t, "carried-in mid kept", m.MidPxLow, 100.5)
		eqF64(t, "high", m.MidPxHigh, 101.5)
		eqInt(t, "ask touch (101 -> 103 not counted: P cleared)", r.AskTouchChanges, 0)
		eqNNBool(t, "engine_overflow", m.EngineOverflow, true)
	})
	t.Run("ResetInstrument", func(t *testing.T) {
		g, s := setup(t)
		g.ResetInstrument("A")
		must(t, g.OnBBO(1.5e9, q(100, 4, 103, 20))) // no P: seeds (mid 101.5)
		must(t, g.OnBBO(1.8e9, q(100, 5, 103, 20))) // bid added 1
		g.Flush(2e9)
		r := s.tfAt(t, 2, "A")
		eqNN(t, "removed", r.BidL1Removed, 0)
		eqNN(t, "added", r.BidL1Added, 1)
		eqNN(t, "discarded print", r.SellQtyAtBid, 0)
		eqInt(t, "discarded events", r.SellEvents, 0)
		nullF64(t, "previous mid discarded", r.SellQtyGe[0])
		m := s.msAt(t, 2, "A")
		eqF64(t, "no carried-in mid: low = 101.5 of the new frames only", m.MidPxLow, 101.5)
		eqInt(t, "bbo_updates", m.BboUpdates, 2)
	})
}

// TestMicroLayout (R27): the grid fills only the layout's active entries;
// New validates the layout and owns a copy.
func TestMicroLayout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Layout = rowsink.Layout{DepthBandsBps: []float64{50, 1000}, LadderEdgesBps: []float64{1000}}
	s := &recSink{}
	g, err := New(cfg, []string{"A"}, s)
	must(t, err)
	cfg.Layout.LadderEdgesBps[0] = 0.5 // must not reach the running grid
	must(t, g.OnBBO(1e9, q(9, 1, 11, 1)))
	must(t, g.OnL2Book(1e9, mkL2("A", 1, []feed.Level{{Px: 9.5, Sz: 2}, {Px: 9, Sz: 4}}, []feed.Level{{Px: 11, Sz: 1}})))
	g.Flush(1e9)
	must(t, g.OnTrades(1.5e9, []feed.Trade{sell(9, 1, 1), sell(9.99, 2, 2)}))
	g.Flush(2e9)
	r := s.msAt(t, 1, "A")
	// mid 10: 50 bps thr 9.95 -> nothing; 1000 bps thr 9.0 -> 9.5*2 + 9*4 = 55.
	eqF64(t, "band 50", r.DepthBid[0], 0)
	eqF64(t, "band 1000", r.DepthBid[1], 55)
	nullF64(t, "inactive band", r.DepthBid[2])
	tf := s.tfAt(t, 2, "A")
	eqF64(t, "edge 1000 (only px 9)", tf.SellQtyGe[0], 1)
	nullF64(t, "inactive edge", tf.SellQtyGe[1])

	for _, bad := range []rowsink.Layout{
		{DepthBandsBps: []float64{2, 1}, LadderEdgesBps: []float64{1}},
		{DepthBandsBps: []float64{1}},
		{DepthBandsBps: []float64{1}, LadderEdgesBps: []float64{0.001}},
	} {
		c := DefaultConfig()
		c.Layout = bad
		if _, err := New(c, []string{"A"}, s); err == nil {
			t.Errorf("New accepted layout %v", bad)
		}
	}
	c := DefaultConfig()
	c.Layout = rowsink.Layout{}
	g, err = New(c, []string{"A"}, s)
	must(t, err)
	if !g.cfg.Layout.Equal(rowsink.DefaultLayout()) {
		t.Errorf("zero layout resolved to %v", g.cfg.Layout)
	}
}
