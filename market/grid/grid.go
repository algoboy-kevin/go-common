// Package grid implements the frame-driven 1 s emission grid and the
// per-instrument market state of the engine (docs/DATA_POINTS.md sections
// 2.1, 2.1.1 rules R1-R15 and 2.2).
//
// The grid consumes decoded data frames in global receive order (rx_ns
// non-decreasing, rule R1) and emits one rowsink.MarketState and one
// rowsink.TradeFlow row per universe instrument per integer second T in
// [ceil(min_rx/1e9), floor(max_rx/1e9)] (rules R2/R3). All time arithmetic
// is integer nanoseconds; no wall clock is ever consulted.
//
// Float determinism: on arm64 the compiler fuses x*y+z into an FMA, which
// changes the last bit versus amd64 and the Python oracle. Every product
// that feeds a sum or a difference is therefore wrapped in an explicit
// float64(...) conversion. Verified with `go tool compile -S` on arm64:
// the bare form compiles to FMADDD, the wrapped form to FMULD+FADDD.
package grid

import (
	"errors"
	"fmt"

	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// MetaEvent is the kind of a _meta recorder sentinel (rule R11).
type MetaEvent uint8

const (
	MetaDisconnected MetaEvent = iota // feed gap opens at the event time
	MetaResubscribed                  // gap closing starts at the event time
	MetaOther                         // ignored (e.g. subscribed/stopped)
)

// Sentinel errors returned by the frame handlers.
var (
	ErrOutOfOrder        = errors.New("grid: rx_ns violates the R1 watermark")
	ErrUnknownInstrument = errors.New("grid: instrument not in universe")
	errBadConfig         = errors.New("grid: invalid config")
	errBadUniverse       = errors.New("grid: invalid universe")
	errBadMetaChannel    = errors.New("grid: OnMeta channel must be KindL2Book/KindBBO/KindTrades/KindAssetCtx")
)

// FundingSchedule is one instrument's funding settlement clock (rule R10):
// settlements occur at every epoch second t with
// (t - AnchorS) mod IntervalS == 0. IntervalS == 0 means no schedule (spot,
// or a venue without funding): funding_interval_s and time_to_settle_s are
// then both null. A schedule with IntervalS > 0 has 0 <= AnchorS < IntervalS.
type FundingSchedule struct{ IntervalS, AnchorS int64 }

// Config carries the R7 staleness limits, the R14 dedup window size and the
// R10 funding schedules. The same knobs drive validity and R11 gap closing
// (b) (ruling 2026-10-30).
type Config struct {
	Layout                                                 rowsink.Layout
	MaxBBOStalenessMs, MaxL2StalenessMs, MaxCtxStalenessMs int64                      // defaults 2000, 15000, 5000 (R7)
	TidWindow                                              int                        // default 4096 (R14)
	DefaultFunding                                         FundingSchedule            // default R10 schedule (Hyperliquid: hourly at :00)
	Funding                                                map[string]FundingSchedule // per-instrument R10 override
	SettledWaitS                                           int64                      // default 600: settled_wait_s of the R20 queue deadline
}

// DefaultConfig returns the R7/R10/R14 defaults used by the golden captures.
func DefaultConfig() Config {
	return Config{
		Layout:            rowsink.DefaultLayout(),
		MaxBBOStalenessMs: 2000,
		MaxL2StalenessMs:  15000,
		MaxCtxStalenessMs: 5000,
		TidWindow:         4096,
		DefaultFunding:    FundingSchedule{IntervalS: 3600, AnchorS: 0},
		SettledWaitS:      600,
	}
}

// Stats are cumulative engine counters.
type Stats struct {
	SecondsEmitted    int64
	RowsEmitted       int64
	OutOfOrder        int64
	UnknownInstrument int64
	DuplicateTids     int64
	MetaBeforeStart   int64
}

// Grid is the frame-driven 1 s emission engine. It is not safe for
// concurrent use and rxNs must be non-decreasing across ALL calls.
type Grid struct {
	cfg   Config
	insts []inst
	index map[string]int
	sink  rowsink.Sink

	started    bool
	nextT      int64 // next boundary to emit (epoch seconds)
	lastRxNs   int64
	haveLastRx bool
	haveWM     bool
	watermark  int64 // W*1e9 of the last emitted boundary

	msRow rowsink.MarketState // reused per row; the sink must copy
	tfRow rowsink.TradeFlow   // reused per row; the sink must copy

	// funding_settlement state (rules R16-R20), active only after
	// EnableFunding. See funding.go.
	fundOn       bool
	fundAny      bool // at least one scheduled instrument
	fundSink     rowsink.FundingSink
	fundSrc      SettledSource
	fnextCloseNs int64 // smallest open funding period end (ns)
	fqueue       fundQueue
	fRow         rowsink.FundingSettlement // reused per row; the sink must copy
	minRxNs      int64                     // rx of the slice's first data frame

	stats Stats
}

// New builds a grid over the given universe (row order follows universe
// order, rule R15). The universe must be non-empty with no empty or
// duplicate names; the sink must be non-nil.
func New(cfg Config, universe []string, sink rowsink.Sink) (*Grid, error) {
	var err error
	cfg.Layout, err = rowsink.ResolveLayout(cfg.Layout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errBadConfig, err)
	}
	if sink == nil {
		return nil, fmt.Errorf("%w: nil sink", errBadConfig)
	}
	// A zero/negative knob cannot be overridden per call and would silently
	// break validity or dedup; refuse it instead of defaulting it.
	if cfg.MaxBBOStalenessMs <= 0 || cfg.MaxL2StalenessMs <= 0 || cfg.MaxCtxStalenessMs <= 0 || cfg.TidWindow <= 0 {
		return nil, fmt.Errorf("%w: staleness limits and TidWindow must be positive", errBadConfig)
	}
	if cfg.SettledWaitS < 0 {
		return nil, fmt.Errorf("%w: SettledWaitS must be >= 0, got %d", errBadConfig, cfg.SettledWaitS)
	}
	if len(universe) == 0 {
		return nil, fmt.Errorf("%w: empty universe", errBadUniverse)
	}
	g := &Grid{
		cfg:   cfg,
		sink:  sink,
		insts: make([]inst, len(universe)),
		index: make(map[string]int, len(universe)),
	}
	for i, name := range universe {
		if name == "" {
			return nil, fmt.Errorf("%w: empty instrument name at index %d", errBadUniverse, i)
		}
		if _, dup := g.index[name]; dup {
			return nil, fmt.Errorf("%w: duplicate instrument %q", errBadUniverse, name)
		}
		g.index[name] = i
		g.insts[i].name = name
	}
	// R10 funding schedules: validate once and resolve each instrument's
	// schedule into its state, so emission does no map lookups.
	if err := validateFunding("default", cfg.DefaultFunding); err != nil {
		return nil, err
	}
	for name, s := range cfg.Funding {
		i, ok := g.index[name]
		if !ok {
			return nil, fmt.Errorf("%w: funding override for %q: instrument not in universe", errBadConfig, name)
		}
		if err := validateFunding(name, s); err != nil {
			return nil, err
		}
		g.insts[i].fund = s
	}
	for i := range g.insts {
		if _, overridden := cfg.Funding[g.insts[i].name]; !overridden {
			g.insts[i].fund = cfg.DefaultFunding
		}
	}
	return g, nil
}

// validateFunding checks one schedule: IntervalS >= 0, and a schedule with
// IntervalS > 0 has 0 <= AnchorS < IntervalS.
func validateFunding(name string, s FundingSchedule) error {
	if s.IntervalS < 0 {
		return fmt.Errorf("%w: funding interval for %q must be >= 0, got %d", errBadConfig, name, s.IntervalS)
	}
	if s.IntervalS > 0 && (s.AnchorS < 0 || s.AnchorS >= s.IntervalS) {
		return fmt.Errorf("%w: funding anchor for %q must be in [0, %d), got %d", errBadConfig, name, s.IntervalS, s.AnchorS)
	}
	return nil
}

// Stats returns cumulative counters.
func (g *Grid) Stats() Stats { return g.stats }

// checkRx enforces the R1 watermark: rxNs must be non-decreasing across all
// calls and must not fall into an already-emitted second. A violating call
// is counted and ignored.
func (g *Grid) checkRx(rxNs int64) bool {
	if (g.haveLastRx && rxNs < g.lastRxNs) || (g.haveWM && rxNs <= g.watermark) {
		g.stats.OutOfOrder++
		return false
	}
	g.lastRxNs = rxNs
	g.haveLastRx = true
	return true
}

// Start starts the global row range without applying data or emitting rows.
// Repeated calls only enforce receive-time monotonicity, including the emission
// watermark. Intake uses it to start quiet shards at the first global data rx.
func (g *Grid) Start(rxNs int64) error {
	if !g.checkRx(rxNs) {
		return ErrOutOfOrder
	}
	if !g.started {
		g.started = true
		g.nextT = ceilSec(rxNs)
		g.minRxNs = rxNs
		g.fundStart(rxNs)
	}
	return nil
}

// dataBegin validates the watermark, starts the grid on the first data
// frame (R2) and emits every boundary strictly before applying the frame.
func (g *Grid) dataBegin(rxNs int64) error {
	if err := g.Start(rxNs); err != nil {
		return err
	}
	g.emitBefore(rxNs)
	g.fundingAdvance(rxNs, false) // close funding periods with S*1e9 < rxNs
	return nil
}

// OnEvent applies one decoded frame from the venue-neutral feed contract
// (feed.FrameDecoder output): data frames are dispatched to the matching
// typed On* method; every other kind returns nil and has no effect. Zero
// allocations.
func (g *Grid) OnEvent(rxNs int64, ev *feed.Event) error {
	switch ev.Kind {
	case feed.KindL2Book:
		return g.OnL2Book(rxNs, &ev.L2)
	case feed.KindBBO:
		return g.OnBBO(rxNs, &ev.BBO)
	case feed.KindTrades:
		return g.OnTrades(rxNs, ev.Trades)
	case feed.KindAssetCtx:
		return g.OnAssetCtx(rxNs, &ev.Ctx)
	}
	return nil
}

// OnL2Book applies one decoded l2Book frame.
func (g *Grid) OnL2Book(rxNs int64, f *feed.L2Book) error {
	if err := g.dataBegin(rxNs); err != nil {
		return err
	}
	i, ok := g.index[f.Coin]
	if !ok {
		g.stats.UnknownInstrument++
		return ErrUnknownInstrument
	}
	in := &g.insts[i]
	in.hasL2 = true
	in.l2NBids, in.l2NAsks = f.NBids, f.NAsks
	in.l2Bids, in.l2Asks = f.Bids, f.Asks
	in.l2RxNs, in.l2TimeMs = rxNs, f.TimeMs
	in.gaps[chL2].noteFrame(rxNs)
	return nil
}

// OnBBO applies one decoded bbo frame.
func (g *Grid) OnBBO(rxNs int64, f *feed.BBO) error {
	if err := g.dataBegin(rxNs); err != nil {
		return err
	}
	i, ok := g.index[f.Coin]
	if !ok {
		g.stats.UnknownInstrument++
		return ErrUnknownInstrument
	}
	in := &g.insts[i]
	in.microBBO(f) // R21/R25 against P, before the raw fields become N
	in.hasBBO = true
	in.bboHasBid, in.bboHasAsk = f.HasBid, f.HasAsk
	in.bboBidPx, in.bboBidSz, in.bboBidN = f.Bid.Px, f.Bid.Sz, f.Bid.N
	in.bboAskPx, in.bboAskSz, in.bboAskN = f.Ask.Px, f.Ask.Sz, f.Ask.N
	in.bboRxNs, in.bboTimeMs = rxNs, f.TimeMs
	in.gaps[chBBO].noteFrame(rxNs)
	return nil
}

// OnAssetCtx applies one decoded activeAssetCtx frame. The frame carries no
// venue time (ruling 2026-10-30), so last_ctx_ms is always null.
func (g *Grid) OnAssetCtx(rxNs int64, f *feed.AssetCtx) error {
	if err := g.dataBegin(rxNs); err != nil {
		return err
	}
	i, ok := g.index[f.Coin]
	if !ok {
		g.stats.UnknownInstrument++
		return ErrUnknownInstrument
	}
	in := &g.insts[i]
	in.hasCtx = true
	in.ctxHave = f.Have
	in.ctxFunding, in.ctxPremium = f.Funding, f.Premium
	in.ctxMark, in.ctxOracle = f.MarkPx, f.OraclePx
	in.ctxOI, in.ctxDayNtl = f.OpenInterest, f.DayNtlVlm
	in.ctxImpactBid, in.ctxImpactAsk = f.ImpactBid, f.ImpactAsk
	in.ctxHasImpact = f.HasImpact
	in.ctxRxNs = rxNs
	in.gaps[chCtx].noteFrame(rxNs)
	if in.fs != nil {
		// funding_settlement accumulation (rules R16/R17) and its own
		// period-window gap list (R11/R19 bit 8).
		in.fs.addCtx(rxNs, f)
		in.fs.gaps.noteFrame(rxNs)
	}
	return nil
}

// OnTrades applies one decoded trades frame. Prints are routed by coin;
// coins outside the universe are counted and skipped. Prints are counted in
// the second ceil(rxNs/1e9) (rule R4) and deduplicated by tid (rule R14).
func (g *Grid) OnTrades(rxNs int64, prints []feed.Trade) error {
	if err := g.dataBegin(rxNs); err != nil {
		return err
	}
	for k := 0; k < len(prints); k++ {
		t := &prints[k]
		i, ok := g.index[t.Coin]
		if !ok {
			g.stats.UnknownInstrument++
			continue
		}
		in := &g.insts[i]
		in.gaps[chTrades].noteFrame(rxNs)
		if in.tids == nil {
			in.tids = newTidWindow(g.cfg.TidWindow) // lazy: first trade of this instrument
		}
		if !in.tids.accept(t.Tid) {
			in.trade.dupDropped++
			g.stats.DuplicateTids++
			continue
		}
		g.microPrint(in, rxNs, t)
		a := &in.trade
		a.nPrints++
		// The notional product is computed once into n (multi-use, so it
		// cannot be fused) and every product that feeds a running sum is
		// additionally wrapped in float64(...) — FMA determinism, see the
		// package comment.
		n := float64(t.Px * t.Sz)
		// feed.Trade.Side is 'B' (buy) or 'A' (sell) per the feed
		// contract; anything else is treated as a sell.
		if t.Side == 'B' {
			a.buyEvents++
			a.buyUSD += n
			a.buyQty += t.Sz
		} else {
			a.sellEvents++
			a.sellUSD += n
			a.sellQty += t.Sz
		}
		if n > a.maxTradeUSD {
			a.maxTradeUSD = n
		}
		a.sumNotionalSq += float64(n * n) // product feeds the running sum: FMA barrier
		if a.nPrints == 1 {
			a.firstMs = t.TimeMs
		}
		a.lastMs = t.TimeMs
		a.lastPx = t.Px
	}
	return nil
}

// OnMeta applies a _meta sentinel for one channel (rule R11). Event time is
// rxNs. A sentinel never starts the grid and never emits rows: the row range
// comes from DATA frames only (rule R2). Deferring emission is exact, because
// a gap opened at m only overlaps seconds with T*1e9 >= m, and a close of
// resub+limit (or later) can never change the overlap of a second ending
// before the resubscribed event.
func (g *Grid) OnMeta(rxNs int64, channel feed.Kind, event MetaEvent) error {
	c := chanIndex(channel)
	if c < 0 {
		return errBadMetaChannel
	}
	if !g.checkRx(rxNs) {
		return ErrOutOfOrder
	}
	if !g.started {
		g.stats.MetaBeforeStart++
	}
	// R25: a disconnected/resubscribed event on bbo OR trades clears P, cb
	// and ca of every instrument (the caller orders the event after every
	// data frame with rx_ns == ts_ns, rank 4).
	if (c == chBBO || c == chTrades) && (event == MetaDisconnected || event == MetaResubscribed) {
		for i := range g.insts {
			g.insts[i].micro.clearTracker()
		}
	}
	switch event {
	case MetaDisconnected:
		for i := range g.insts {
			g.insts[i].gaps[c].openAt(rxNs)
			if c == chCtx {
				g.fundOpenAt(i, rxNs)
			}
		}
	case MetaResubscribed:
		limit := g.channelLimitNs(c)
		for i := range g.insts {
			g.insts[i].gaps[c].resubAt(rxNs, limit)
			if c == chCtx {
				g.fundResubAt(i, rxNs, limit)
			}
		}
	case MetaOther:
		// ignored
	}
	return nil
}

// channelLimitNs is the R11 closing-(b) limit for one channel: the
// configured R7 staleness threshold for l2Book, bbo and activeAssetCtx, and 0
// for trades, whose gap closes at the resubscribed event itself (DATA_POINTS
// R11: trades are sparse, so a staleness limit would flag healthy seconds).
// This is what capture_gap pins down byte for byte.
func (g *Grid) channelLimitNs(c int) int64 {
	switch c {
	case chL2:
		return g.cfg.MaxL2StalenessMs * 1e6
	case chBBO:
		return g.cfg.MaxBBOStalenessMs * 1e6
	case chCtx:
		return g.cfg.MaxCtxStalenessMs * 1e6
	default: // chTrades
		return 0
	}
}

// emitBefore emits every boundary T with T*1e9 < rxNs, in order.
func (g *Grid) emitBefore(rxNs int64) {
	for g.nextT*1e9 < rxNs {
		g.emitSecond(g.nextT)
		g.nextT++
	}
}

// Flush emits every not-yet-emitted boundary T with T*1e9 <= untilNs.
// Replay end is Flush(maxDataRx), so rows run through floor(max_rx/1e9)
// (rules R2/R4); prints after that boundary are dropped at end of input.
func (g *Grid) Flush(untilNs int64) {
	if !g.started {
		return
	}
	for g.nextT*1e9 <= untilNs {
		g.emitSecond(g.nextT)
		g.nextT++
	}
	g.fundingAdvance(untilNs, true) // close funding periods with S*1e9 <= untilNs
}

// emitSecond emits both rows for every universe instrument (in universe
// order) for boundary T, then closes the per-second trade bucket and drops
// gaps that can never overlap a later second again.
func (g *Grid) emitSecond(T int64) {
	for i := range g.insts {
		in := &g.insts[i]
		g.fillMarketState(&g.msRow, in, T)
		g.sink.MarketState(&g.msRow)
		g.fillTradeFlow(&g.tfRow, in, T)
		g.sink.TradeFlow(&g.tfRow)
		in.trade = tradeAcc{}               // per-second trade bucket (R4 window)
		in.micro.resetWindow(g.msRow.MidPx) // R21 carry-in, R24 previous mid
	}
	// For every later T' > T the window starts at T*1e9, so a gap with
	// close <= T*1e9 can never overlap again (task semantics 6).
	bound := T * 1e9
	for i := range g.insts {
		g.insts[i].overflow.dropClosed(bound)
		for c := 0; c < numChans; c++ {
			g.insts[i].gaps[c].dropClosed(bound)
		}
	}
	g.stats.SecondsEmitted++
	g.stats.RowsEmitted += 2 * int64(len(g.insts))
	g.watermark = bound
	g.haveWM = true
}

// ceilSec returns ceil(ns/1e9): ns exactly on a boundary belongs to that
// second (counts in T, not T+1).
func ceilSec(ns int64) int64 {
	q, r := ns/1e9, ns%1e9
	if r > 0 {
		q++
	}
	return q
}
