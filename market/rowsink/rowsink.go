// Package rowsink defines the output row types and the sink interface of the
// engine. It is the contract between the grid (producer) and the writers
// (consumers: CSV debug sink, later Parquet). Parent-owned: children code
// against it and do not change it; a missing field is a contract bug to report.
//
// Semantics of every field are in docs/DATA_POINTS.md sections 2.1, 2.1.1
// (rules R1-R15) and 2.2. A nullable value is a wrapper with Ok=false meaning
// null (never zero). Rows are passed by pointer and are only valid for the
// duration of the call: a sink must copy what it keeps.
package rowsink

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// F64 is a nullable float64 (Ok=false means null; V is then 0 and meaningless).
type F64 struct {
	V  float64
	Ok bool
}

// I64 is a nullable int64.
type I64 struct {
	V  int64
	Ok bool
}

// Bool is a nullable bool.
type Bool struct {
	V  bool
	Ok bool
}

// Some builds a non-null F64.
func Some(v float64) F64 { return F64{V: v, Ok: true} }

// SomeI builds a non-null I64.
func SomeI(v int64) I64 { return I64{V: v, Ok: true} }

// SomeB builds a non-null Bool.
func SomeB(v bool) Bool { return Bool{V: v, Ok: true} }

// MidSrc says where mid_px came from (rule R8).
type MidSrc uint8

const (
	MidNone MidSrc = iota // mid_px is null
	MidBBO                // from a valid bbo
	MidL2                 // fallback to the latest valid l2Book
)

// String returns the CSV spelling: "none", "bbo" or "l2".
func (m MidSrc) String() string {
	switch m {
	case MidBBO:
		return "bbo"
	case MidL2:
		return "l2"
	default:
		return "none"
	}
}

// Maximum number of configurable depth bands / ladder edges. Row structs hold
// fixed arrays of this size so rows never allocate; the active count comes from
// the run's Layout.
const (
	MaxDepthBands  = 16
	MaxLadderEdges = 24
)

// Layout is the run-time shape of the two configurable column families
// (rule R27): the cumulative depth bands of market_state_1s (rule R9) and the
// distance-ladder edges of trade_flow_1s (rule R24). Both are lists of basis
// points, strictly increasing. The defaults are DefaultLayout().
type Layout struct {
	DepthBandsBps  []float64
	LadderEdgesBps []float64
}

// DefaultDepthBandsBps and DefaultLadderEdgesBps are the shipped defaults.
var (
	DefaultDepthBandsBps  = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000}
	DefaultLadderEdgesBps = []float64{0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000}
)

// DefaultLayout returns a fresh copy of the default layout.
func DefaultLayout() Layout {
	return Layout{
		DepthBandsBps:  append([]float64(nil), DefaultDepthBandsBps...),
		LadderEdgesBps: append([]float64(nil), DefaultLadderEdgesBps...),
	}
}

// Validate reports every problem at once (rule R27): each list non-empty, at
// most MaxDepthBands / MaxLadderEdges long, strictly increasing, every value
// in [0.01, 10000] bps with at most two decimals (so names and Python/Go
// parsing are exact).
func (l Layout) Validate() error {
	var errs []error
	check := func(name string, v []float64, max int) {
		if len(v) == 0 || len(v) > max {
			errs = append(errs, fmt.Errorf("%s: need 1..%d values, got %d", name, max, len(v)))
		}
		for i, x := range v {
			if math.IsNaN(x) || x < 0.01 || x > 10000 {
				errs = append(errs, fmt.Errorf("%s[%d]=%v: must be within [0.01, 10000] bps", name, i, x))
				continue
			}
			if math.Abs(x*100-math.Round(x*100)) > 1e-9 {
				errs = append(errs, fmt.Errorf("%s[%d]=%v: at most two decimals", name, i, x))
			}
			if i > 0 && !(x > v[i-1]) {
				errs = append(errs, fmt.Errorf("%s[%d]=%v: must be strictly greater than the previous value %v", name, i, x, v[i-1]))
			}
		}
	}
	check("depth_bands_bps", l.DepthBandsBps, MaxDepthBands)
	check("ladder_edges_bps", l.LadderEdgesBps, MaxLadderEdges)
	return errors.Join(errs...)
}

// Equal reports whether two layouts are identical.
func (l Layout) Equal(o Layout) bool {
	return slices.Equal(l.DepthBandsBps, o.DepthBandsBps) && slices.Equal(l.LadderEdgesBps, o.LadderEdgesBps)
}

// String is the canonical text of the layout (fingerprints, file metadata):
// "depth=1,2,5;ladder=0.5,1" with shortest decimal numbers.
func (l Layout) String() string {
	f := func(v []float64) string {
		p := make([]string, len(v))
		for i, x := range v {
			p[i] = strconv.FormatFloat(x, 'f', -1, 64)
		}
		return strings.Join(p, ",")
	}
	return "depth=" + f(l.DepthBandsBps) + ";ladder=" + f(l.LadderEdgesBps)
}

// BandSuffix is the column-name form of a bps value (rule R27): the value with
// two decimals, trailing zeros and a trailing point removed, the point
// replaced by "p": 10 -> "10", 0.5 -> "0p5", 12.25 -> "12p25".
func BandSuffix(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return strings.Replace(s, ".", "p", 1)
}

// flow_attrib_flags bits (rule R26).
const (
	FlowUnclassified    int64 = 1 // some print could not be classified (R22)
	FlowNoBBOUpdate     int64 = 2 // prints in the second but no bbo update
	FlowTradeAdjustedL1 int64 = 4 // an L1 update was adjusted by trades (R25)
)

// Quality bit positions (rule R12).
const (
	QBBONever     uint16 = 1 << 0
	QBBOStale     uint16 = 1 << 1
	QBBOOneSided  uint16 = 1 << 2
	QBBOCrossed   uint16 = 1 << 3 // crossed, locked or zero size
	QL2Never      uint16 = 1 << 4
	QL2Stale      uint16 = 1 << 5
	QL2BadBook    uint16 = 1 << 6 // empty side or crossed
	QCtxNever     uint16 = 1 << 7
	QCtxStale     uint16 = 1 << 8
	QFeedGap      uint16 = 1 << 9
	QOverflow     uint16 = 1 << 10
	QTradeFeedBad uint16 = 1 << 11
)

// MarketState is one row of market_state_1s (docs/DATA_POINTS.md 2.1).
// Field order matches MarketStateColumns.
type MarketState struct {
	TsS        int64  // UTC second end, epoch seconds
	Instrument string // venue name, e.g. "xyz:JP225" (interned universe string)

	BidPx, AskPx, BidSz, AskSz F64
	BidN, AskN                 I64

	MidPx  F64
	MidSrc MidSrc

	// R21: range of the bbo mid during the second (incl. the carried-in mid)
	// and the number of bbo frames received in it.
	MidPxHigh, MidPxLow F64
	BboUpdates          int64 // never null

	SpreadBps, MicroPx, MicroMinusMidBps, ImbalanceL1 F64

	DepthBid, DepthAsk       [MaxDepthBands]F64 // USD notional within Layout.DepthBandsBps[i] bps (R9); a lower bound when L2Reach < that band
	L2ReachBid, L2ReachAsk   F64                // bps from mid to the farthest returned level (R9)
	L2LevelsBid, L2LevelsAsk I64

	BboAgeMs, L2AgeMs, CtxAgeMs    I64
	LastBboMs, LastL2Ms, LastCtxMs I64

	MarkPx, OraclePx, FundingRate, Premium, OpenInterest, OpenInterestUSD F64
	ImpactBidPx, ImpactAskPx, DayNtlVlmUSD                                F64
	MarkOracleBps, MidOracleBps                                           F64

	// R10: both come from the instrument's configured funding schedule and are
	// null together when it has none (spot, or a venue without funding).
	// FundingRate is per FundingIntervalS and is NOT normalized to an hour.
	FundingIntervalS, TimeToSettleS I64 // time_to_settle_s range 1..interval

	BboValid, BookValid, CtxValid, Valid, FeedGap, EngineOverflow bool // never null
	Quality                                                       uint16
}

// TradeFlow is one row of trade_flow_1s (docs/DATA_POINTS.md 2.2).
// Field order matches TradeFlowColumns.
type TradeFlow struct {
	TsS        int64
	Instrument string

	BuyEvents, SellEvents int64 // never null; 0 means none observed (see TradeFeedValid)

	BuyUSD, SellUSD, BuyQty, SellQty, MaxTradeUSD, SumNotionalSq float64 // never null

	Vwap                       F64
	FirstTradeMs, LastTradeMs  I64
	LastTradePx, FlowImbalance F64

	DupDropped     int64 // never null
	TradeFeedValid bool  // never null

	// R23 extremes of the deduplicated prints.
	SellPxMin, BuyPxMax F64

	// R22 classification against the prevailing bbo at print time (base qty).
	SellQtyAtBid, SellQtyThruBid, BuyQtyAtAsk, BuyQtyThruAsk float64 // never null
	SellQtyUnclassified, BuyQtyUnclassified                  float64 // never null

	// R25 touch-level flow not explained by trades (base qty) and touch moves.
	BidL1Removed, BidL1Added, AskL1Removed, AskL1Added float64 // never null
	BidTouchChanges, AskTouchChanges                   int64   // never null
	FlowAttribFlags                                    int64   // FlowUnclassified|... never null

	// R24 distance ladder (cumulative base qty at least E bps beyond the
	// previous second's mid); null when that reference mid is null.
	SellQtyGe, BuyQtyGe [MaxLadderEdges]F64 // index i = Layout.LadderEdgesBps[i]
}

// MarketStateColumns / TradeFlowColumns are the column lists of the DEFAULT
// layout (67 and 56 columns). Layout-aware code uses Layout.MarketStateColumns
// and Layout.TradeFlowColumns.
var (
	MarketStateColumns = DefaultLayout().MarketStateColumns()
	TradeFlowColumns   = DefaultLayout().TradeFlowColumns()
)

// MarketStateColumns is the exact CSV header / column order of market_state_1s
// (rule R15) for this layout: 47 + 2*len(DepthBandsBps) columns.
func (l Layout) MarketStateColumns() []string {
	c := []string{
		"ts_s", "instrument",
		"bid_px", "ask_px", "bid_sz", "ask_sz",
		"bid_n", "ask_n",
		"mid_px", "mid_src",
		"mid_px_high", "mid_px_low", "bbo_updates",
		"spread_bps", "micro_px", "micro_minus_mid_bps",
		"imbalance_l1",
	}
	for _, side := range []string{"bid", "ask"} {
		for _, b := range l.DepthBandsBps {
			c = append(c, "depth_"+side+"_"+BandSuffix(b)+"bps_usd")
		}
	}
	return append(c,
		"l2_reach_bid_bps", "l2_reach_ask_bps",
		"l2_levels_bid", "l2_levels_ask",
		"bbo_age_ms", "l2_age_ms", "ctx_age_ms",
		"last_bbo_ms", "last_l2_ms", "last_ctx_ms",
		"mark_px", "oracle_px", "funding_rate",
		"premium", "open_interest", "open_interest_usd",
		"impact_bid_px", "impact_ask_px", "day_ntl_vlm_usd",
		"mark_oracle_bps", "mid_oracle_bps",
		"funding_interval_s", "time_to_settle_s",
		"bbo_valid", "book_valid", "ctx_valid",
		"valid", "feed_gap", "engine_overflow",
		"quality",
	)
}

// TradeFlowColumns is the exact CSV header / column order of trade_flow_1s
// (rule R15) for this layout: 32 + 2*len(LadderEdgesBps) columns.
func (l Layout) TradeFlowColumns() []string {
	c := []string{
		"ts_s", "instrument",
		"buy_events", "sell_events",
		"buy_usd", "sell_usd", "buy_qty", "sell_qty",
		"max_trade_usd", "sum_notional_sq",
		"vwap", "first_trade_ms", "last_trade_ms",
		"last_trade_px", "flow_imbalance",
		"dup_dropped", "trade_feed_valid",
		"sell_px_min", "buy_px_max",
		"sell_qty_at_bid", "sell_qty_thru_bid", "buy_qty_at_ask", "buy_qty_thru_ask",
		"sell_qty_unclassified", "buy_qty_unclassified",
		"bid_l1_removed_qty", "bid_l1_added_qty", "ask_l1_removed_qty", "ask_l1_added_qty",
		"bid_touch_changes", "ask_touch_changes",
		"flow_attrib_flags",
	}
	for _, side := range []string{"sell", "buy"} {
		for _, e := range l.LadderEdgesBps {
			c = append(c, side+"_qty_ge_"+BandSuffix(e)+"bps")
		}
	}
	return c
}

// FundingSettlement is one row of funding_settlement (docs/DATA_POINTS.md
// 2.3 and rules R16-R20). Field order matches FundingSettlementColumns.
// FundingRate-like values are per FundingIntervalS and are not normalized.
type FundingSettlement struct {
	TsS        int64 // settlement second S, the end of the period (S-I, S]
	Instrument string

	FundingIntervalS int64 // never null

	FundingSettled                                               F64
	FundingCtxLast, FundingCtxMean, FundingCtxMin, FundingCtxMax F64
	FundingChanges                                               int64 // never null
	PremiumMean, PremiumLast                                     F64
	OraclePxSettle, MarkPxSettle                                 F64
	OIUSDStart, OIUSDEnd                                         F64

	CtxFrames     int64 // never null; 0 means no ctx frame in the period
	CtxGapMaxMs   I64
	ReconcileDiff F64

	SourceFlags uint16 // FundFlag* bits (R19)
}

// funding_settlement source_flags bits (rule R19).
const (
	FundFlagSettledNotFetched uint16 = 1  // no settled data for the instrument
	FundFlagSettledMissing    uint16 = 2  // settled data supplied, no matching entry
	FundFlagPartialStart      uint16 = 4  // data began after the period started
	FundFlagFeedGap           uint16 = 8  // an activeAssetCtx R11 gap overlaps the period
	FundFlagNoCtx             uint16 = 16 // no ctx frame in the period
	FundFlagReconcileMismatch uint16 = 32 // funding_ctx_last != funding_settled
	FundFlagEngineOverflow    uint16 = 64 // reserved: a queue drop touched the instrument
)

// FundingSettlementColumns is the exact CSV header / column order of
// funding_settlement (rule R15 applies). 19 columns.
var FundingSettlementColumns = []string{
	"ts_s", "instrument", "funding_interval_s",
	"funding_settled", "funding_ctx_last", "funding_ctx_mean", "funding_ctx_min", "funding_ctx_max",
	"funding_changes",
	"premium_mean", "premium_last",
	"oracle_px_settle", "mark_px_settle",
	"oi_usd_start", "oi_usd_end",
	"ctx_frames", "ctx_gap_max_ms",
	"reconcile_diff", "source_flags",
}

// FundingSink consumes funding_settlement rows. Same rules as Sink: do not
// retain the pointer; rows arrive in increasing TsS, and for equal TsS in
// universe order (rule R15).
type FundingSink interface {
	FundingSettlement(r *FundingSettlement)
}

// Sink consumes engine output rows. Implementations must not retain the
// pointers after the call returns. Calls for one instrument arrive in
// increasing TsS; across instruments the order within a second follows the
// universe order (rule R15).
type Sink interface {
	MarketState(r *MarketState)
	TradeFlow(r *TradeFlow)
}
