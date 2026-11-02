package grid

// Hand-built rule tests for R2-R14 and the watermark/emission traps. Float
// expectations use dyadic values so exact == comparisons are meaningful;
// bit-exact arithmetic against the oracle is gated by the golden tests.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// recSink records copied rows for assertions.
type recSink struct {
	ms []rowsink.MarketState
	tf []rowsink.TradeFlow
}

func (s *recSink) MarketState(r *rowsink.MarketState) { s.ms = append(s.ms, *r) }
func (s *recSink) TradeFlow(r *rowsink.TradeFlow)     { s.tf = append(s.tf, *r) }

func (s *recSink) msAt(t *testing.T, T int64, instrument string) *rowsink.MarketState {
	t.Helper()
	for i := range s.ms {
		if s.ms[i].TsS == T && s.ms[i].Instrument == instrument {
			return &s.ms[i]
		}
	}
	t.Fatalf("no market_state row for %s at T=%d", instrument, T)
	return nil
}

func (s *recSink) tfAt(t *testing.T, T int64, instrument string) *rowsink.TradeFlow {
	t.Helper()
	for i := range s.tf {
		if s.tf[i].TsS == T && s.tf[i].Instrument == instrument {
			return &s.tf[i]
		}
	}
	t.Fatalf("no trade_flow row for %s at T=%d", instrument, T)
	return nil
}

// ---- frame builders (decoded shapes; the feed package is trusted) ----

func mkL2(coin string, timeMs int64, bids, asks []feed.Level) *feed.L2Book {
	f := &feed.L2Book{Coin: coin, TimeMs: timeMs, NBids: len(bids), NAsks: len(asks)}
	copy(f.Bids[:], bids)
	copy(f.Asks[:], asks)
	return f
}

func mkBBO(coin string, timeMs int64, hasBid, hasAsk bool, bid, ask feed.Level) *feed.BBO {
	return &feed.BBO{Coin: coin, TimeMs: timeMs, HasBid: hasBid, HasAsk: hasAsk, Bid: bid, Ask: ask}
}

func mkTrade(coin string, side uint8, px, sz float64, timeMs, tid int64) feed.Trade {
	return feed.Trade{Coin: coin, Side: side, Px: px, Sz: sz, TimeMs: timeMs, Tid: tid}
}

func mkCtx(coin string, mark, oracle, oi float64) *feed.AssetCtx {
	return &feed.AssetCtx{Coin: coin, Have: feed.CtxAll, MarkPx: mark, OraclePx: oracle, OpenInterest: oi,
		DayNtlVlm: 5000, ImpactBid: mark - 0.25, ImpactAsk: mark + 0.25, HasImpact: true}
}

// ---- assertion helpers ----

func eqF64(t *testing.T, name string, got rowsink.F64, want float64) {
	t.Helper()
	if !got.Ok {
		t.Fatalf("%s: null, want %v", name, want)
	}
	if got.V != want {
		t.Fatalf("%s: got %v, want %v", name, got.V, want)
	}
}

func nullF64(t *testing.T, name string, got rowsink.F64) {
	t.Helper()
	if got.Ok {
		t.Fatalf("%s: got %v, want null", name, got.V)
	}
}

func eqI64(t *testing.T, name string, got rowsink.I64, want int64) {
	t.Helper()
	if !got.Ok {
		t.Fatalf("%s: null, want %d", name, want)
	}
	if got.V != want {
		t.Fatalf("%s: got %d, want %d", name, got.V, want)
	}
}

func nullI64(t *testing.T, name string, got rowsink.I64) {
	t.Helper()
	if got.Ok {
		t.Fatalf("%s: got %d, want null", name, got.V)
	}
}

func eqOptBool(t *testing.T, name string, got rowsink.Bool, want bool) {
	t.Helper()
	if !got.Ok {
		t.Fatalf("%s: null, want %v", name, want)
	}
	if got.V != want {
		t.Fatalf("%s: got %v, want %v", name, got.V, want)
	}
}

func nullBool(t *testing.T, name string, got rowsink.Bool) {
	t.Helper()
	if got.Ok {
		t.Fatalf("%s: got %v, want null", name, got.V)
	}
}

func eqNNBool(t *testing.T, name string, got, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
}

// goodBBO/goodL2 return a consistent pair around mid 100.25 (dyadic).
func goodBBO(coin string, timeMs int64) *feed.BBO {
	return mkBBO(coin, timeMs, true, true,
		feed.Level{Px: 100, Sz: 2, N: 1}, feed.Level{Px: 100.25, Sz: 4, N: 2})
}

func goodL2(coin string, timeMs int64) *feed.L2Book {
	return mkL2(coin, timeMs,
		[]feed.Level{{Px: 100, Sz: 2, N: 1}},
		[]feed.Level{{Px: 100.25, Sz: 4, N: 2}})
}

// ---- rules ----

// TestRuleFrameOnBoundaryCountsInT: a frame at rx exactly T*1e9 is applied
// before T is emitted and counted in T, not T+1 (rules R2/R4 trap).
func TestRuleFrameOnBoundaryCountsInT(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnL2Book(100e9, goodL2("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnTrades(100e9, []feed.Trade{mkTrade("A", 'B', 100, 1, 100000, 1)}); err != nil {
		t.Fatal(err)
	}
	g.Flush(100e9)
	if len(sink.ms) != 1 || len(sink.tf) != 1 {
		t.Fatalf("after Flush(100e9): %d ms rows, %d tf rows, want 1 and 1", len(sink.ms), len(sink.tf))
	}
	row := sink.msAt(t, 100, "A")
	eqI64(t, "bbo_age_ms@100", row.BboAgeMs, 0) // counted in T: age 0, not 1000
	tf := sink.tfAt(t, 100, "A")
	if tf.BuyEvents != 1 {
		t.Fatalf("buy_events@100 = %d, want 1", tf.BuyEvents)
	}
	eqI64(t, "first_trade_ms@100", tf.FirstTradeMs, 100000)
	g.Flush(101e9)
	eqI64(t, "bbo_age_ms@101", sink.msAt(t, 101, "A").BboAgeMs, 1000)
	if tf := sink.tfAt(t, 101, "A"); tf.BuyEvents != 0 {
		t.Fatalf("buy_events@101 = %d, want 0 (print belongs to T=100)", tf.BuyEvents)
	}
}

// TestRuleStartEndRangeAndQuietInstruments (R2/R3): rows cover
// [ceil(min_rx/1e9), floor(max_rx/1e9)] and instruments without frames
// still get rows, in universe order.
func TestRuleStartEndRangeAndQuietInstruments(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A", "B"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100.2e9, goodBBO("A", 100200)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnL2Book(102.7e9, goodL2("A", 102700)); err != nil {
		t.Fatal(err)
	}
	g.Flush(102.7e9)
	if len(sink.ms) != 4 {
		t.Fatalf("got %d ms rows, want 4 (T=101,102 x A,B)", len(sink.ms))
	}
	wantOrder := []struct {
		T    int64
		name string
	}{{101, "A"}, {101, "B"}, {102, "A"}, {102, "B"}}
	for i, w := range wantOrder {
		if sink.ms[i].TsS != w.T || sink.ms[i].Instrument != w.name {
			t.Fatalf("row %d = (%d,%s), want (%d,%s)", i, sink.ms[i].TsS, sink.ms[i].Instrument, w.T, w.name)
		}
	}
	eqI64(t, "bbo_age@101", sink.msAt(t, 101, "A").BboAgeMs, 800)
	eqI64(t, "bbo_age@102", sink.msAt(t, 102, "A").BboAgeMs, 1800)
	b := sink.msAt(t, 101, "B") // quiet instrument: rows still emitted (R3)
	if b.Quality != rowsink.QBBONever|rowsink.QL2Never|rowsink.QCtxNever {
		t.Fatalf("quiet quality = %d, want 145", b.Quality)
	}
	eqNNBool(t, "quiet valid", b.Valid, false)
}

// TestRuleRawSurvivesStaleness (R6): raw columns keep the last known frame
// content (including absent sides) while derived columns go null.
func TestRuleRawSurvivesStaleness(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnL2Book(100e9, goodL2("A", 100000)); err != nil {
		t.Fatal(err)
	}
	// One-sided follow-up: the absent bid side must NOT be forward-filled.
	one := mkBBO("A", 101500, false, true, feed.Level{}, feed.Level{Px: 100.3, Sz: 5, N: 2})
	if err := g.OnBBO(101.5e9, one); err != nil {
		t.Fatal(err)
	}
	g.Flush(104e9)
	r := sink.msAt(t, 104, "A")   // bbo age 2500 > 2000: stale, still raw
	nullF64(t, "bid_px", r.BidPx) // absent in the last frame, not forward-filled
	nullF64(t, "bid_sz", r.BidSz)
	nullI64(t, "bid_n", r.BidN)
	eqF64(t, "ask_px raw", r.AskPx, 100.3)
	eqF64(t, "ask_sz raw", r.AskSz, 5)
	eqI64(t, "ask_n raw", r.AskN, 2)
	eqNNBool(t, "bbo_valid", r.BboValid, false)
	nullF64(t, "spread_bps (derived)", r.SpreadBps)
	nullF64(t, "micro_px (derived)", r.MicroPx)
	nullF64(t, "imbalance_l1 (derived)", r.ImbalanceL1)
	eqI64(t, "l2_levels raw", r.L2LevelsBid, 1) // l2 raw survives too (age 4000 < 15000)
}

// TestRuleAgeAtExactThresholdValid (R7): age exactly at the limit is valid
// for all three channels.
func TestRuleAgeAtExactThresholdValid(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnL2Book(100e9, goodL2("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000)); err != nil {
		t.Fatal(err)
	}
	g.Flush(116e9)
	r := sink.msAt(t, 102, "A") // bbo age 2000 == limit
	eqI64(t, "bbo_age@102", r.BboAgeMs, 2000)
	eqNNBool(t, "bbo_valid@102", r.BboValid, true)
	if !r.SpreadBps.Ok {
		t.Fatal("spread_bps must be computed while bbo_valid")
	}
	eqNNBool(t, "bbo_valid@103", sink.msAt(t, 103, "A").BboValid, false) // 3000 > 2000
	r = sink.msAt(t, 105, "A")                                           // ctx age 5000 == limit
	eqI64(t, "ctx_age@105", r.CtxAgeMs, 5000)
	eqNNBool(t, "ctx_valid@105", r.CtxValid, true)
	eqNNBool(t, "ctx_valid@106", sink.msAt(t, 106, "A").CtxValid, false) // 6000 > 5000
	r = sink.msAt(t, 115, "A")                                           // l2 age 15000 == limit
	eqI64(t, "l2_age@115", r.L2AgeMs, 15000)
	eqNNBool(t, "book_valid@115", r.BookValid, true)
	eqNNBool(t, "book_valid@116", sink.msAt(t, 116, "A").BookValid, false) // 16000 > 15000
}

// TestRuleMidFallbackToL2 (R8): bbo mid, l2 fallback, then none.
func TestRuleMidFallbackToL2(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnL2Book(100e9, goodL2("A", 100000)); err != nil {
		t.Fatal(err)
	}
	crossed := mkBBO("A", 101000, true, true,
		feed.Level{Px: 100.5, Sz: 2, N: 1}, feed.Level{Px: 100.25, Sz: 4, N: 2})
	if err := g.OnBBO(101e9, crossed); err != nil {
		t.Fatal(err)
	}
	g.Flush(130e9)
	r := sink.msAt(t, 100, "A")
	if r.MidSrc != rowsink.MidBBO {
		t.Fatalf("mid_src@100 = %v, want bbo", r.MidSrc)
	}
	eqF64(t, "mid_px@100", r.MidPx, 100.125) // (100+100.25)/2
	r = sink.msAt(t, 101, "A")               // crossed bbo: fallback to l2
	if r.MidSrc != rowsink.MidL2 {
		t.Fatalf("mid_src@101 = %v, want l2", r.MidSrc)
	}
	eqF64(t, "mid_px@101", r.MidPx, 100.125)
	r = sink.msAt(t, 130, "A") // everything stale: no valid price source
	if r.MidSrc != rowsink.MidNone {
		t.Fatalf("mid_src@130 = %v, want none", r.MidSrc)
	}
	nullF64(t, "mid_px@130", r.MidPx)
	nullF64(t, "depth_bid_50bps@130", r.DepthBid[5]) // no mid -> depth null
}

// TestRuleDepthReachPerSideAndEmptySide (R9).
func TestRuleDepthReachPerSideAndEmptySide(t *testing.T) {
	// Dyadic levels at known bps offsets from mid 100.25 (size 2 keeps all
	// products and sums exact): bids 3.12/6.24/10.9/21.8 bps below mid and
	// asks at the same offsets above, so band N accumulates exactly N levels.
	bids := []feed.Level{
		{Px: 100.21875, Sz: 2},  // 3.12 bps
		{Px: 100.1875, Sz: 2},   // 6.24
		{Px: 100.140625, Sz: 2}, // 10.9
		{Px: 100.03125, Sz: 2},  // 21.8
	}
	asks := []feed.Level{
		{Px: 100.28125, Sz: 2},  // 3.12 bps above
		{Px: 100.3125, Sz: 2},   // 6.24
		{Px: 100.359375, Sz: 2}, // 10.9
		{Px: 100.46875, Sz: 2},  // 21.8
	}

	t.Run("bands and trunc per side", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		// bbo mid = (100 + 100.5)/2 = 100.25
		if err := g.OnBBO(100e9, mkBBO("A", 100000, true, true,
			feed.Level{Px: 100, Sz: 2, N: 1}, feed.Level{Px: 100.5, Sz: 4, N: 2})); err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(100e9, mkL2("A", 100000, bids, asks)); err != nil {
			t.Fatal(err)
		}
		g.Flush(100e9)
		r := sink.msAt(t, 100, "A")
		eqF64(t, "depth_bid_5bps", r.DepthBid[2], 200.4375) // 100.21875*2
		eqF64(t, "depth_bid_10bps", r.DepthBid[3], 400.8125)
		eqF64(t, "depth_bid_20bps", r.DepthBid[4], 601.09375)
		eqF64(t, "depth_bid_50bps", r.DepthBid[5], 801.15625)
		eqF64(t, "depth_ask_5bps", r.DepthAsk[2], 200.5625) // 100.28125*2
		eqF64(t, "depth_ask_10bps", r.DepthAsk[3], 401.1875)
		eqF64(t, "depth_ask_20bps", r.DepthAsk[4], 601.90625)
		eqF64(t, "depth_ask_50bps", r.DepthAsk[5], 802.84375)
		eqF64(t, "l2_reach_bid_bps", r.L2ReachBid, 1e4*(100.25-100.03125)/100.25) // 1e4*(mid - deepest bid)/mid
		eqF64(t, "l2_reach_ask_bps", r.L2ReachAsk, 1e4*(100.46875-100.25)/100.25) // 1e4*(deepest ask - mid)/mid
	})

	t.Run("empty side: trunc null, sums zero", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		// bbo mid = 100.25; only the bid side has levels.
		if err := g.OnBBO(100e9, mkBBO("A", 100000, true, true,
			feed.Level{Px: 100, Sz: 2, N: 1}, feed.Level{Px: 100.5, Sz: 4, N: 2})); err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(100e9, mkL2("A", 100000, []feed.Level{{Px: 100, Sz: 2}}, nil)); err != nil {
			t.Fatal(err)
		}
		g.Flush(100e9)
		r := sink.msAt(t, 100, "A")
		eqF64(t, "l2_reach_bid_bps (levels present)", r.L2ReachBid, 1e4*(100.25-100)/100.25) // 1e4*(mid - deepest bid)/mid
		nullF64(t, "l2_reach_ask_bps (no levels)", r.L2ReachAsk)
		// R9 computes the sums whenever an l2 frame exists and mid is not
		// null: an empty side sums to 0.0 (only trunc is null per side).
		eqF64(t, "depth_bid_50bps", r.DepthBid[5], 200)
		eqF64(t, "depth_ask_50bps", r.DepthAsk[5], 0)
		eqNNBool(t, "book_valid (empty side)", r.BookValid, false)
		if r.Quality&rowsink.QL2BadBook == 0 {
			t.Fatalf("quality %d missing QL2BadBook for empty side", r.Quality)
		}
	})

	t.Run("no mid: all depth null", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(100e9, mkL2("A", 100000, bids, asks)); err != nil {
			t.Fatal(err)
		}
		g.Flush(130e9) // l2 stale at 130: no valid price source -> mid null
		r := sink.msAt(t, 130, "A")
		nullF64(t, "mid_px", r.MidPx)
		for b := 0; b < len(rowsink.DefaultLayout().DepthBandsBps); b++ {
			nullF64(t, "depth_bid", r.DepthBid[b])
			nullF64(t, "depth_ask", r.DepthAsk[b])
		}
		nullF64(t, "l2_reach_bid_bps", r.L2ReachBid)
		nullF64(t, "l2_reach_ask_bps", r.L2ReachAsk)
	})
}

// TestRuleSettleWrapAtHourEdge (R10).
func TestRuleSettleWrapAtHourEdge(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(1791223198e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	g.Flush(1791223201e9)
	want := []int64{2, 1, 3600, 3599} // 1791223200 is top of hour (T%3600 == 0)
	for i, w := range want {
		T := int64(1791223198 + i)
		eqI64(t, fmt.Sprintf("time_to_settle_s@%d", T), sink.msAt(t, T, "A").TimeToSettleS, w)
	}
}

// TestRuleGapCloseConfiguredThreshold (R11): closing (b) follows the
// configured MaxL2StalenessMs, closing (a) is per instrument, quiet
// instruments close by (b) only, two consecutive disconnects are unioned,
// and a gap without resubscribed stays open.
func TestRuleGapCloseConfiguredThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxL2StalenessMs = 3000 // non-default: close (b) = resub + 3000ms

	t.Run("close (b) uses configured threshold", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(cfg, []string{"A", "B"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(98e9, goodL2("A", 1)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(100e9, feed.KindL2Book, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(101e9, feed.KindL2Book, MetaResubscribed); err != nil {
			t.Fatal(err)
		}
		g.Flush(106e9)
		// gap [100e9, 101e9+3e9) = [100e9, 104e9): flags T=100..104.
		for _, name := range []string{"A", "B"} {
			for T := int64(98); T <= 106; T++ {
				want := T >= 100 && T <= 104
				eqNNBool(t, "feed_gap", sink.msAt(t, T, name).FeedGap, want)
			}
			eqNNBool(t, "trade_feed_valid", sink.tfAt(t, 101, name).TradeFeedValid, true) // trades channel untouched
		}
	})

	t.Run("close (a) is per instrument", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(cfg, []string{"A", "B"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(98e9, goodL2("A", 1)); err != nil { // row range starts at 98
			t.Fatal(err)
		}
		if err := g.OnMeta(100e9, feed.KindL2Book, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(101e9, feed.KindL2Book, MetaResubscribed); err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(102.5e9, goodL2("A", 1)); err != nil { // rule (a) for A only
			t.Fatal(err)
		}
		g.Flush(106e9)
		// A: close min(104e9, 102.5e9) = 102.5e9 -> flags 100..103.
		for T := int64(100); T <= 103; T++ {
			eqNNBool(t, "A feed_gap", sink.msAt(t, T, "A").FeedGap, true)
		}
		eqNNBool(t, "A feed_gap@104", sink.msAt(t, 104, "A").FeedGap, false)
		// B is quiet: close (b) only -> flags 100..104.
		for T := int64(100); T <= 104; T++ {
			eqNNBool(t, "B feed_gap", sink.msAt(t, T, "B").FeedGap, true)
		}
		eqNNBool(t, "B feed_gap@105", sink.msAt(t, 105, "B").FeedGap, false)
	})

	t.Run("two consecutive disconnects are unioned", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(cfg, []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(98e9, goodL2("A", 1)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(100e9, feed.KindL2Book, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(100.5e9, feed.KindL2Book, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(101e9, feed.KindL2Book, MetaResubscribed); err != nil {
			t.Fatal(err)
		}
		g.Flush(106e9)
		// Both gaps reference the next resubscribed: [100e9,104e9) and
		// [100.5e9,104e9), union flags 100..104 and closes for good.
		for T := int64(100); T <= 104; T++ {
			eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, true)
		}
		for T := int64(105); T <= 106; T++ {
			eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, false)
		}
	})

	t.Run("gap without resubscribed stays open", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(cfg, []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(98e9, goodL2("A", 1)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(100e9, feed.KindL2Book, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		g.Flush(106e9)
		for T := int64(100); T <= 106; T++ {
			eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, true)
		}
	})

	t.Run("trades gap closes at resubscribed and flags trade_feed_valid", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(cfg, []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnL2Book(98e9, goodL2("A", 1)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(100e9, feed.KindTrades, MetaDisconnected); err != nil {
			t.Fatal(err)
		}
		if err := g.OnMeta(101e9, feed.KindTrades, MetaResubscribed); err != nil {
			t.Fatal(err)
		}
		g.Flush(105e9)
		// gap [100e9, 101e9): the trades close is the resubscribed event
		// itself (see channelLimitNs — the golden gate overrides the task
		// file's "2000 ms trades"): flags T=100..101 for feed_gap and
		// trade_feed_valid=false (capture_gap t-9/t-8 pattern).
		for T := int64(100); T <= 101; T++ {
			eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, true)
			eqNNBool(t, "trade_feed_valid", sink.tfAt(t, T, "A").TradeFeedValid, false)
		}
		for T := int64(102); T <= 105; T++ {
			eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, false)
			eqNNBool(t, "trade_feed_valid", sink.tfAt(t, T, "A").TradeFeedValid, true)
		}
	})
}

// TestMetaBeforeStartKeepsGap: OnMeta before the first data frame must not
// start the grid but must keep its gap state (task semantics 1).
func TestMetaBeforeStartKeepsGap(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnMeta(90e9, feed.KindBBO, MetaDisconnected); err != nil {
		t.Fatal(err)
	}
	if st := g.Stats(); st.MetaBeforeStart != 1 {
		t.Fatalf("MetaBeforeStart = %d, want 1", st.MetaBeforeStart)
	}
	g.Flush(95e9)
	if st := g.Stats(); st.SecondsEmitted != 0 {
		t.Fatalf("OnMeta started the grid: SecondsEmitted = %d", st.SecondsEmitted)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	g.Flush(101e9)
	for _, T := range []int64{100, 101} {
		eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, true) // stored gap applies
	}
}

// TestRuleQualityBits (R12): each failure mode sets exactly its bit.
func TestRuleQualityBits(t *testing.T) {
	// run asserts A's quality at second T. B is the quiet grid-starter: the
	// "missing" case feeds only B, and every case gets a trailing B frame at
	// T*1e9 so the row range (floor(max_rx/1e9), R2/R3) reaches T without
	// touching A's state.
	run := func(t *testing.T, want uint16, T int64, feed func(g *Grid)) {
		t.Helper()
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A", "B"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		feed(g)
		if err := g.OnBBO(T*1e9, goodBBO("B", 1)); err != nil {
			t.Fatal(err)
		}
		g.Flush(T * 1e9)
		if got := sink.msAt(t, T, "A").Quality; got != want {
			t.Fatalf("quality@%d = %d, want %d", T, got, want)
		}
	}
	t.Run("missing everything sets only bits 0/4/7", func(t *testing.T) {
		run(t, rowsink.QBBONever|rowsink.QL2Never|rowsink.QCtxNever, 100, func(g *Grid) {
			// A never receives anything; B's frame starts the grid.
			_ = g.OnBBO(100e9, goodBBO("B", 1))
		})
	})
	t.Run("stale bbo with good content sets only bit1", func(t *testing.T) {
		run(t, rowsink.QBBOStale, 103, func(g *Grid) {
			_ = g.OnBBO(100e9, goodBBO("A", 1))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("one-sided bbo sets only bit2", func(t *testing.T) {
		run(t, rowsink.QBBOOneSided, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, mkBBO("A", 1, false, true, feed.Level{}, feed.Level{Px: 100.25, Sz: 4, N: 1}))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("crossed bbo sets bit3", func(t *testing.T) {
		run(t, rowsink.QBBOCrossed, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, mkBBO("A", 1, true, true,
				feed.Level{Px: 100.5, Sz: 2, N: 1}, feed.Level{Px: 100.25, Sz: 4, N: 1}))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("zero size bbo sets bit3", func(t *testing.T) {
		run(t, rowsink.QBBOCrossed, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, mkBBO("A", 1, true, true,
				feed.Level{Px: 100, Sz: 0, N: 1}, feed.Level{Px: 100.25, Sz: 4, N: 1}))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("stale l2 sets only bit5", func(t *testing.T) {
		run(t, rowsink.QL2Stale, 116, func(g *Grid) {
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnBBO(115.5e9, goodBBO("A", 1))
			_ = g.OnAssetCtx(115.5e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("empty l2 side sets bit6", func(t *testing.T) {
		run(t, rowsink.QL2BadBook, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, goodBBO("A", 1))
			_ = g.OnL2Book(100e9, mkL2("A", 1, []feed.Level{{Px: 100, Sz: 2}}, nil))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
		})
	})
	t.Run("stale ctx sets only bit8", func(t *testing.T) {
		run(t, rowsink.QCtxStale, 106, func(g *Grid) {
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
			_ = g.OnBBO(105.5e9, goodBBO("A", 1))
			_ = g.OnL2Book(105.5e9, goodL2("A", 1))
		})
	})
	t.Run("channel gap sets bit9 only", func(t *testing.T) {
		run(t, rowsink.QFeedGap, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, goodBBO("A", 1))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
			_ = g.OnMeta(100e9, feed.KindL2Book, MetaDisconnected)
		})
	})
	t.Run("trades gap sets bit9 and bit11", func(t *testing.T) {
		run(t, rowsink.QFeedGap|rowsink.QTradeFeedBad, 100, func(g *Grid) {
			_ = g.OnBBO(100e9, goodBBO("A", 1))
			_ = g.OnL2Book(100e9, goodL2("A", 1))
			_ = g.OnAssetCtx(100e9, mkCtx("A", 100.25, 100, 1000))
			_ = g.OnMeta(100e9, feed.KindTrades, MetaDisconnected)
		})
	})
}

// TestRuleValidConjunction (R13).
func TestRuleValidConjunction(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 1)); err != nil { // bbo only, no l2
		t.Fatal(err)
	}
	if err := g.OnL2Book(101e9, goodL2("A", 1)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnAssetCtx(101e9, mkCtx("A", 100.25, 100, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnMeta(102e9, feed.KindBBO, MetaDisconnected); err != nil { // gap at 102
		t.Fatal(err)
	}
	if err := g.OnBBO(103e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	g.Flush(140e9)
	eqNNBool(t, "valid bbo-only", sink.msAt(t, 100, "A").Valid, true)
	eqNNBool(t, "valid gap", sink.msAt(t, 102, "A").Valid, false)             // feed_gap blocks
	eqNNBool(t, "valid fresh after gap", sink.msAt(t, 103, "A").Valid, false) // gap open (no resub)
	eqNNBool(t, "valid stale", sink.msAt(t, 140, "A").Valid, false)           // no valid price source
}

// TestRuleTidWindowEvictionAndDuplicate (R14).
func TestRuleTidWindowEvictionAndDuplicate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TidWindow = 2
	sink := &recSink{}
	g, err := New(cfg, []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	// Arrival order: tid1, tid2, tid3, tid1(evicted -> re-accepted), tid3(dup).
	prints := []feed.Trade{
		mkTrade("A", 'B', 100, 1, 501, 1),
		mkTrade("A", 'A', 100, 2, 502, 2),
		mkTrade("A", 'B', 100.5, 1, 503, 3),
		mkTrade("A", 'B', 100, 1, 500, 1), // tid1 re-accepted after eviction
		mkTrade("A", 'A', 100, 5, 505, 3), // tid3 still in window: dropped
	}
	if err := g.OnTrades(100.5e9, prints); err != nil {
		t.Fatal(err)
	}
	// Second bucket: tid3 dup again (still in window {3,1}), tid4 new.
	if err := g.OnTrades(101.5e9, []feed.Trade{
		mkTrade("A", 'A', 100, 5, 505, 3),
		mkTrade("A", 'B', 100, 1, 506, 4),
	}); err != nil {
		t.Fatal(err)
	}
	g.Flush(102e9)
	r := sink.tfAt(t, 101, "A")
	if r.BuyEvents != 3 || r.SellEvents != 1 {
		t.Fatalf("events = %d/%d, want 3/1 (dup tid3 dropped, tid1 re-accepted)", r.BuyEvents, r.SellEvents)
	}
	if r.DupDropped != 1 {
		t.Fatalf("dup_dropped@101 = %d, want 1", r.DupDropped)
	}
	if r.BuyUSD != 300.5 {
		t.Fatalf("buy_usd = %v, want 300.5 (100 + 100.5 + 100)", r.BuyUSD)
	}
	if r.SellUSD != 200 {
		t.Fatalf("sell_usd = %v, want 200 (only tid2)", r.SellUSD)
	}
	eqI64(t, "first_trade_ms", r.FirstTradeMs, 501)
	eqI64(t, "last_trade_ms", r.LastTradeMs, 500) // arrival order, not venue time
	eqF64(t, "last_trade_px", r.LastTradePx, 100)
	r = sink.tfAt(t, 102, "A")
	if r.DupDropped != 1 || r.BuyEvents != 1 || r.SellEvents != 0 {
		t.Fatalf("bucket 2 = dup %d, %d/%d events; want 1 dup, 1/0 (tid4 buys)", r.DupDropped, r.BuyEvents, r.SellEvents)
	}
	if st := g.Stats(); st.DuplicateTids != 2 {
		t.Fatalf("DuplicateTids = %d, want 2", st.DuplicateTids)
	}
}

// TestWatermarkOutOfOrder: rejection after Flush, boundary-before-flush
// acceptance, and the non-decreasing rule.
func TestWatermarkOutOfOrder(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	// Frame at exactly T*1e9 before any flush: accepted and counted in T.
	if err := g.OnBBO(200e9, goodBBO("A", 200000)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnTrades(200e9, []feed.Trade{mkTrade("A", 'B', 100, 1, 200000, 1)}); err != nil {
		t.Fatal(err)
	}
	g.Flush(200e9)
	eqI64(t, "bbo_age@200", sink.msAt(t, 200, "A").BboAgeMs, 0)
	if tf := sink.tfAt(t, 200, "A"); tf.BuyEvents != 1 {
		t.Fatalf("buy_events@200 = %d, want 1", tf.BuyEvents)
	}
	g.Flush(201e9) // watermark now 201e9
	if err := g.OnBBO(201e9, goodBBO("A", 201000)); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("frame at rx == watermark: err = %v, want ErrOutOfOrder", err)
	}
	if err := g.OnBBO(200.5e9, goodBBO("A", 200500)); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("frame at rx < watermark: err = %v, want ErrOutOfOrder", err)
	}
	if err := g.OnL2Book(300.2e9, goodL2("A", 1)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(300.1e9, goodBBO("A", 1)); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("non-decreasing violation: err = %v, want ErrOutOfOrder", err)
	}
	if st := g.Stats(); st.OutOfOrder != 3 {
		t.Fatalf("OutOfOrder = %d, want 3", st.OutOfOrder)
	}
}

// TestUnknownInstrument: counted and skipped; data frames still drive the
// row range.
func TestUnknownInstrument(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("ZZZ", 1)); !errors.Is(err, ErrUnknownInstrument) {
		t.Fatalf("err = %v, want ErrUnknownInstrument", err)
	}
	if err := g.OnTrades(100.5e9, []feed.Trade{mkTrade("ZZZ", 'B', 100, 1, 1, 1)}); err != nil {
		t.Fatalf("OnTrades with unknown coin: err = %v, want nil", err)
	}
	g.Flush(100e9)
	if st := g.Stats(); st.UnknownInstrument != 2 {
		t.Fatalf("UnknownInstrument = %d, want 2", st.UnknownInstrument)
	}
	// Row range covers every non-meta frame (R2), unknown coins included:
	// min_rx=100e9, max_rx=100.5e9 -> T=100 only -> 1 ms + 1 tf row.
	if len(sink.ms) != 1 {
		t.Fatalf("got %d ms rows, want 1 (T=100)", len(sink.ms))
	}
}

// TestNewErrors: universe and config validation.
func TestNewErrors(t *testing.T) {
	sink := &recSink{}
	if _, err := New(DefaultConfig(), nil, sink); !errors.Is(err, errBadUniverse) {
		t.Fatalf("nil universe: err = %v, want errBadUniverse", err)
	}
	if _, err := New(DefaultConfig(), []string{}, sink); !errors.Is(err, errBadUniverse) {
		t.Fatalf("empty universe: err = %v, want errBadUniverse", err)
	}
	if _, err := New(DefaultConfig(), []string{"A", "A"}, sink); !errors.Is(err, errBadUniverse) {
		t.Fatalf("duplicate: err = %v, want errBadUniverse", err)
	}
	if _, err := New(DefaultConfig(), []string{"A", ""}, sink); !errors.Is(err, errBadUniverse) {
		t.Fatalf("empty name: err = %v, want errBadUniverse", err)
	}
	if _, err := New(DefaultConfig(), []string{"A"}, nil); !errors.Is(err, errBadConfig) {
		t.Fatalf("nil sink: err = %v, want errBadConfig", err)
	}
	if _, err := New(Config{}, []string{"A"}, sink); !errors.Is(err, errBadConfig) {
		t.Fatalf("zero config: err = %v, want errBadConfig", err)
	}
}

// TestFlushTwiceIsNoOp: repeated and backward Flush calls emit nothing new.
func TestFlushTwiceIsNoOp(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	g.Flush(105e9)
	n := len(sink.ms)
	if n != 6 {
		t.Fatalf("got %d ms rows, want 6", n)
	}
	g.Flush(105e9)
	g.Flush(104e9)
	g.Flush(99e9)
	if len(sink.ms) != n {
		t.Fatalf("Flush changed row count: %d -> %d", n, len(sink.ms))
	}
}
