package rowsink

import (
	"fmt"
	"math"
	"strconv"
)

// Diff is one differing cell between two row slices (rule R15 comparison).
// Row is the 0-based row index; A and B hold the CSV spellings of the two
// cells (empty string for null). A row-count mismatch is reported as a
// single Diff{Row: -1, Column: "<row count>"}.
type Diff struct {
	Row    int
	Column string
	A, B   string
}

// String formats the diff for logs and test failures.
func (d Diff) String() string {
	return fmt.Sprintf("row %d column %s: %q != %q", d.Row, d.Column, d.A, d.B)
}

// Tolerances from rule R15: integers, bools, strings and MidSrc compare
// exactly (and nullness is always exact); two floats are equal when both
// are null, or both non-null and |x-y| <= 1e-12 or |x-y| <= 1e-9*max(|x|,|y|).
const (
	floatAbsTol = 1e-12
	floatRelTol = 1e-9
)

func floatClose(x, y float64) bool {
	d := math.Abs(x - y)
	if d <= floatAbsTol {
		return true
	}
	m := math.Abs(x)
	if ay := math.Abs(y); ay > m {
		m = ay
	}
	return d <= floatRelTol*m
}

// CompareMarketState compares two market_state_1s row slices positionally
// and returns one Diff per differing cell (all of them), over the columns of
// the given layout (none or the zero Layout = DefaultLayout()). A row-count
// mismatch or an invalid layout returns exactly one Diff (Row -1) and stops.
func CompareMarketState(a, b []MarketState, layouts ...Layout) []Diff {
	if len(a) != len(b) {
		return []Diff{{Row: -1, Column: "<row count>", A: strconv.Itoa(len(a)), B: strconv.Itoa(len(b))}}
	}
	layout := selectedLayout(layouts)
	if err := layout.Validate(); err != nil {
		return []Diff{{Row: -1, Column: "<layout>", A: err.Error()}}
	}
	bidNames, askNames := familyNames(layout.MarketStateColumns(), "imbalance_l1", len(layout.DepthBandsBps))
	var diffs []Diff
	for row := range a {
		ra, rb := &a[row], &b[row]
		if ra.TsS != rb.TsS {
			diffs = append(diffs, Diff{row, "ts_s", intText(ra.TsS), intText(rb.TsS)})
		}
		if ra.Instrument != rb.Instrument {
			diffs = append(diffs, Diff{row, "instrument", ra.Instrument, rb.Instrument})
		}
		diffs = cmpF64(diffs, row, "bid_px", ra.BidPx, rb.BidPx)
		diffs = cmpF64(diffs, row, "ask_px", ra.AskPx, rb.AskPx)
		diffs = cmpF64(diffs, row, "bid_sz", ra.BidSz, rb.BidSz)
		diffs = cmpF64(diffs, row, "ask_sz", ra.AskSz, rb.AskSz)
		diffs = cmpI64(diffs, row, "bid_n", ra.BidN, rb.BidN)
		diffs = cmpI64(diffs, row, "ask_n", ra.AskN, rb.AskN)
		diffs = cmpF64(diffs, row, "mid_px", ra.MidPx, rb.MidPx)
		if ra.MidSrc != rb.MidSrc {
			diffs = append(diffs, Diff{row, "mid_src", ra.MidSrc.String(), rb.MidSrc.String()})
		}
		diffs = cmpF64(diffs, row, "mid_px_high", ra.MidPxHigh, rb.MidPxHigh)
		diffs = cmpF64(diffs, row, "mid_px_low", ra.MidPxLow, rb.MidPxLow)
		if ra.BboUpdates != rb.BboUpdates {
			diffs = append(diffs, Diff{row, "bbo_updates", intText(ra.BboUpdates), intText(rb.BboUpdates)})
		}
		diffs = cmpF64(diffs, row, "spread_bps", ra.SpreadBps, rb.SpreadBps)
		diffs = cmpF64(diffs, row, "micro_px", ra.MicroPx, rb.MicroPx)
		diffs = cmpF64(diffs, row, "micro_minus_mid_bps", ra.MicroMinusMidBps, rb.MicroMinusMidBps)
		diffs = cmpF64(diffs, row, "imbalance_l1", ra.ImbalanceL1, rb.ImbalanceL1)
		for i, name := range bidNames {
			diffs = cmpF64(diffs, row, name, ra.DepthBid[i], rb.DepthBid[i])
		}
		for i, name := range askNames {
			diffs = cmpF64(diffs, row, name, ra.DepthAsk[i], rb.DepthAsk[i])
		}
		diffs = cmpF64(diffs, row, "l2_reach_bid_bps", ra.L2ReachBid, rb.L2ReachBid)
		diffs = cmpF64(diffs, row, "l2_reach_ask_bps", ra.L2ReachAsk, rb.L2ReachAsk)
		diffs = cmpI64(diffs, row, "l2_levels_bid", ra.L2LevelsBid, rb.L2LevelsBid)
		diffs = cmpI64(diffs, row, "l2_levels_ask", ra.L2LevelsAsk, rb.L2LevelsAsk)
		diffs = cmpI64(diffs, row, "bbo_age_ms", ra.BboAgeMs, rb.BboAgeMs)
		diffs = cmpI64(diffs, row, "l2_age_ms", ra.L2AgeMs, rb.L2AgeMs)
		diffs = cmpI64(diffs, row, "ctx_age_ms", ra.CtxAgeMs, rb.CtxAgeMs)
		diffs = cmpI64(diffs, row, "last_bbo_ms", ra.LastBboMs, rb.LastBboMs)
		diffs = cmpI64(diffs, row, "last_l2_ms", ra.LastL2Ms, rb.LastL2Ms)
		diffs = cmpI64(diffs, row, "last_ctx_ms", ra.LastCtxMs, rb.LastCtxMs)
		diffs = cmpF64(diffs, row, "mark_px", ra.MarkPx, rb.MarkPx)
		diffs = cmpF64(diffs, row, "oracle_px", ra.OraclePx, rb.OraclePx)
		diffs = cmpF64(diffs, row, "funding_rate", ra.FundingRate, rb.FundingRate)
		diffs = cmpF64(diffs, row, "premium", ra.Premium, rb.Premium)
		diffs = cmpF64(diffs, row, "open_interest", ra.OpenInterest, rb.OpenInterest)
		diffs = cmpF64(diffs, row, "open_interest_usd", ra.OpenInterestUSD, rb.OpenInterestUSD)
		diffs = cmpF64(diffs, row, "impact_bid_px", ra.ImpactBidPx, rb.ImpactBidPx)
		diffs = cmpF64(diffs, row, "impact_ask_px", ra.ImpactAskPx, rb.ImpactAskPx)
		diffs = cmpF64(diffs, row, "day_ntl_vlm_usd", ra.DayNtlVlmUSD, rb.DayNtlVlmUSD)
		diffs = cmpF64(diffs, row, "mark_oracle_bps", ra.MarkOracleBps, rb.MarkOracleBps)
		diffs = cmpF64(diffs, row, "mid_oracle_bps", ra.MidOracleBps, rb.MidOracleBps)
		diffs = cmpI64(diffs, row, "funding_interval_s", ra.FundingIntervalS, rb.FundingIntervalS)
		diffs = cmpI64(diffs, row, "time_to_settle_s", ra.TimeToSettleS, rb.TimeToSettleS)
		if ra.BboValid != rb.BboValid {
			diffs = append(diffs, Diff{row, "bbo_valid", boolText(ra.BboValid), boolText(rb.BboValid)})
		}
		if ra.BookValid != rb.BookValid {
			diffs = append(diffs, Diff{row, "book_valid", boolText(ra.BookValid), boolText(rb.BookValid)})
		}
		if ra.CtxValid != rb.CtxValid {
			diffs = append(diffs, Diff{row, "ctx_valid", boolText(ra.CtxValid), boolText(rb.CtxValid)})
		}
		if ra.Valid != rb.Valid {
			diffs = append(diffs, Diff{row, "valid", boolText(ra.Valid), boolText(rb.Valid)})
		}
		if ra.FeedGap != rb.FeedGap {
			diffs = append(diffs, Diff{row, "feed_gap", boolText(ra.FeedGap), boolText(rb.FeedGap)})
		}
		if ra.EngineOverflow != rb.EngineOverflow {
			diffs = append(diffs, Diff{row, "engine_overflow", boolText(ra.EngineOverflow), boolText(rb.EngineOverflow)})
		}
		if ra.Quality != rb.Quality {
			diffs = append(diffs, Diff{row, "quality", uintText(uint64(ra.Quality)), uintText(uint64(rb.Quality))})
		}
	}
	return diffs
}

// CompareTradeFlow compares two trade_flow_1s row slices (see
// CompareMarketState).
func CompareTradeFlow(a, b []TradeFlow, layouts ...Layout) []Diff {
	if len(a) != len(b) {
		return []Diff{{Row: -1, Column: "<row count>", A: strconv.Itoa(len(a)), B: strconv.Itoa(len(b))}}
	}
	layout := selectedLayout(layouts)
	if err := layout.Validate(); err != nil {
		return []Diff{{Row: -1, Column: "<layout>", A: err.Error()}}
	}
	sellNames, buyNames := familyNames(layout.TradeFlowColumns(), "flow_attrib_flags", len(layout.LadderEdgesBps))
	var diffs []Diff
	for row := range a {
		ra, rb := &a[row], &b[row]
		if ra.TsS != rb.TsS {
			diffs = append(diffs, Diff{row, "ts_s", intText(ra.TsS), intText(rb.TsS)})
		}
		if ra.Instrument != rb.Instrument {
			diffs = append(diffs, Diff{row, "instrument", ra.Instrument, rb.Instrument})
		}
		if ra.BuyEvents != rb.BuyEvents {
			diffs = append(diffs, Diff{row, "buy_events", intText(ra.BuyEvents), intText(rb.BuyEvents)})
		}
		if ra.SellEvents != rb.SellEvents {
			diffs = append(diffs, Diff{row, "sell_events", intText(ra.SellEvents), intText(rb.SellEvents)})
		}
		diffs = cmpF64NN(diffs, row, "buy_usd", ra.BuyUSD, rb.BuyUSD)
		diffs = cmpF64NN(diffs, row, "sell_usd", ra.SellUSD, rb.SellUSD)
		diffs = cmpF64NN(diffs, row, "buy_qty", ra.BuyQty, rb.BuyQty)
		diffs = cmpF64NN(diffs, row, "sell_qty", ra.SellQty, rb.SellQty)
		diffs = cmpF64NN(diffs, row, "max_trade_usd", ra.MaxTradeUSD, rb.MaxTradeUSD)
		diffs = cmpF64NN(diffs, row, "sum_notional_sq", ra.SumNotionalSq, rb.SumNotionalSq)
		diffs = cmpF64(diffs, row, "vwap", ra.Vwap, rb.Vwap)
		diffs = cmpI64(diffs, row, "first_trade_ms", ra.FirstTradeMs, rb.FirstTradeMs)
		diffs = cmpI64(diffs, row, "last_trade_ms", ra.LastTradeMs, rb.LastTradeMs)
		diffs = cmpF64(diffs, row, "last_trade_px", ra.LastTradePx, rb.LastTradePx)
		diffs = cmpF64(diffs, row, "flow_imbalance", ra.FlowImbalance, rb.FlowImbalance)
		if ra.DupDropped != rb.DupDropped {
			diffs = append(diffs, Diff{row, "dup_dropped", intText(ra.DupDropped), intText(rb.DupDropped)})
		}
		if ra.TradeFeedValid != rb.TradeFeedValid {
			diffs = append(diffs, Diff{row, "trade_feed_valid", boolText(ra.TradeFeedValid), boolText(rb.TradeFeedValid)})
		}
		diffs = cmpF64(diffs, row, "sell_px_min", ra.SellPxMin, rb.SellPxMin)
		diffs = cmpF64(diffs, row, "buy_px_max", ra.BuyPxMax, rb.BuyPxMax)
		diffs = cmpF64NN(diffs, row, "sell_qty_at_bid", ra.SellQtyAtBid, rb.SellQtyAtBid)
		diffs = cmpF64NN(diffs, row, "sell_qty_thru_bid", ra.SellQtyThruBid, rb.SellQtyThruBid)
		diffs = cmpF64NN(diffs, row, "buy_qty_at_ask", ra.BuyQtyAtAsk, rb.BuyQtyAtAsk)
		diffs = cmpF64NN(diffs, row, "buy_qty_thru_ask", ra.BuyQtyThruAsk, rb.BuyQtyThruAsk)
		diffs = cmpF64NN(diffs, row, "sell_qty_unclassified", ra.SellQtyUnclassified, rb.SellQtyUnclassified)
		diffs = cmpF64NN(diffs, row, "buy_qty_unclassified", ra.BuyQtyUnclassified, rb.BuyQtyUnclassified)
		diffs = cmpF64NN(diffs, row, "bid_l1_removed_qty", ra.BidL1Removed, rb.BidL1Removed)
		diffs = cmpF64NN(diffs, row, "bid_l1_added_qty", ra.BidL1Added, rb.BidL1Added)
		diffs = cmpF64NN(diffs, row, "ask_l1_removed_qty", ra.AskL1Removed, rb.AskL1Removed)
		diffs = cmpF64NN(diffs, row, "ask_l1_added_qty", ra.AskL1Added, rb.AskL1Added)
		if ra.BidTouchChanges != rb.BidTouchChanges {
			diffs = append(diffs, Diff{row, "bid_touch_changes", intText(ra.BidTouchChanges), intText(rb.BidTouchChanges)})
		}
		if ra.AskTouchChanges != rb.AskTouchChanges {
			diffs = append(diffs, Diff{row, "ask_touch_changes", intText(ra.AskTouchChanges), intText(rb.AskTouchChanges)})
		}
		if ra.FlowAttribFlags != rb.FlowAttribFlags {
			diffs = append(diffs, Diff{row, "flow_attrib_flags", intText(ra.FlowAttribFlags), intText(rb.FlowAttribFlags)})
		}
		for i, name := range sellNames {
			diffs = cmpF64(diffs, row, name, ra.SellQtyGe[i], rb.SellQtyGe[i])
		}
		for i, name := range buyNames {
			diffs = cmpF64(diffs, row, name, ra.BuyQtyGe[i], rb.BuyQtyGe[i])
		}
	}
	return diffs
}

// CompareFundingSettlement compares two funding_settlement row slices (see
// CompareMarketState).
func CompareFundingSettlement(a, b []FundingSettlement) []Diff {
	if len(a) != len(b) {
		return []Diff{{Row: -1, Column: "<row count>", A: strconv.Itoa(len(a)), B: strconv.Itoa(len(b))}}
	}
	var diffs []Diff
	for i := range a {
		ra, rb := &a[i], &b[i]
		row := i
		c := FundingSettlementColumns
		if ra.TsS != rb.TsS {
			diffs = append(diffs, Diff{row, c[0], intText(ra.TsS), intText(rb.TsS)})
		}
		if ra.Instrument != rb.Instrument {
			diffs = append(diffs, Diff{row, c[1], ra.Instrument, rb.Instrument})
		}
		diffs = cmpI64(diffs, row, c[2], rowsinkI64(ra.FundingIntervalS), rowsinkI64(rb.FundingIntervalS))
		diffs = cmpF64(diffs, row, c[3], ra.FundingSettled, rb.FundingSettled)
		diffs = cmpF64(diffs, row, c[4], ra.FundingCtxLast, rb.FundingCtxLast)
		diffs = cmpF64(diffs, row, c[5], ra.FundingCtxMean, rb.FundingCtxMean)
		diffs = cmpF64(diffs, row, c[6], ra.FundingCtxMin, rb.FundingCtxMin)
		diffs = cmpF64(diffs, row, c[7], ra.FundingCtxMax, rb.FundingCtxMax)
		if ra.FundingChanges != rb.FundingChanges {
			diffs = append(diffs, Diff{row, c[8], intText(ra.FundingChanges), intText(rb.FundingChanges)})
		}
		diffs = cmpF64(diffs, row, c[9], ra.PremiumMean, rb.PremiumMean)
		diffs = cmpF64(diffs, row, c[10], ra.PremiumLast, rb.PremiumLast)
		diffs = cmpF64(diffs, row, c[11], ra.OraclePxSettle, rb.OraclePxSettle)
		diffs = cmpF64(diffs, row, c[12], ra.MarkPxSettle, rb.MarkPxSettle)
		diffs = cmpF64(diffs, row, c[13], ra.OIUSDStart, rb.OIUSDStart)
		diffs = cmpF64(diffs, row, c[14], ra.OIUSDEnd, rb.OIUSDEnd)
		if ra.CtxFrames != rb.CtxFrames {
			diffs = append(diffs, Diff{row, c[15], intText(ra.CtxFrames), intText(rb.CtxFrames)})
		}
		diffs = cmpI64(diffs, row, c[16], ra.CtxGapMaxMs, rb.CtxGapMaxMs)
		diffs = cmpF64(diffs, row, c[17], ra.ReconcileDiff, rb.ReconcileDiff)
		if ra.SourceFlags != rb.SourceFlags {
			diffs = append(diffs, Diff{row, c[18], uintText(uint64(ra.SourceFlags)), uintText(uint64(rb.SourceFlags))})
		}
	}
	return diffs
}

// rowsinkI64 wraps a never-null int64 as I64 for cmpI64 (exact compare).
// familyNames returns the names of a two-sided configurable column family
// (depth bands or ladder edges) as the layout's column builder spells them:
// the n columns right after column after, then the next n. Taking them from
// the builder list keeps Compare's names identical to the CSV header.
func familyNames(cols []string, after string, n int) (first, second []string) {
	for i, c := range cols {
		if c == after {
			return cols[i+1 : i+1+n], cols[i+1+n : i+1+2*n]
		}
	}
	panic("rowsink: column " + after + " missing from the builder list")
}

func rowsinkI64(v int64) I64 { return I64{V: v, Ok: true} }

func cmpF64(diffs []Diff, row int, col string, a, b F64) []Diff {
	if a.Ok != b.Ok {
		return append(diffs, Diff{row, col, f64Text(a), f64Text(b)})
	}
	if a.Ok && !floatClose(a.V, b.V) {
		return append(diffs, Diff{row, col, f64Text(a), f64Text(b)})
	}
	return diffs
}

func cmpF64NN(diffs []Diff, row int, col string, a, b float64) []Diff {
	if !floatClose(a, b) {
		return append(diffs, Diff{row, col, FormatFloat(a), FormatFloat(b)})
	}
	return diffs
}

func cmpI64(diffs []Diff, row int, col string, a, b I64) []Diff {
	if a.Ok != b.Ok || (a.Ok && a.V != b.V) {
		return append(diffs, Diff{row, col, i64Text(a), i64Text(b)})
	}
	return diffs
}

func cmpBool(diffs []Diff, row int, col string, a, b Bool) []Diff {
	if a.Ok != b.Ok || (a.Ok && a.V != b.V) {
		return append(diffs, Diff{row, col, boolOptText(a), boolOptText(b)})
	}
	return diffs
}

func f64Text(v F64) string {
	if !v.Ok {
		return ""
	}
	return FormatFloat(v.V)
}

func i64Text(v I64) string {
	if !v.Ok {
		return ""
	}
	return strconv.FormatInt(v.V, 10)
}

func boolOptText(v Bool) string {
	if !v.Ok {
		return ""
	}
	return boolText(v.V)
}

func intText(v int64) string   { return strconv.FormatInt(v, 10) }
func uintText(v uint64) string { return strconv.FormatUint(v, 10) }
func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
