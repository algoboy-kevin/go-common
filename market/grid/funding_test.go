package grid

// Hand-built rule R10 tests: per-instrument funding schedules and ctx
// presence flags. Expectations are hand-computed from the settlement
// calendar (settlements at every epoch second t with (t-anchor) mod interval
// == 0), not from the implementation formula.

import (
	"fmt"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
)

// fundRun builds a grid with cfg over universe, feeds one bbo frame per
// instrument at startRxNs and flushes to flushToNs, returning the rows.
func fundRun(t *testing.T, cfg Config, universe []string, startRxNs, flushToNs int64) *recSink {
	t.Helper()
	sink := &recSink{}
	g, err := New(cfg, universe, sink)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range universe {
		if err := g.OnBBO(startRxNs, goodBBO(name, startRxNs/1e6)); err != nil {
			t.Fatal(err)
		}
	}
	g.Flush(flushToNs)
	return sink
}

// TestFundingSchedule8hAtAnchorZero (C.7a): schedule 28800/0. Settlements at
// T = 0, 28800, 57600, ...; time_to_settle_s counts down 28800 -> 1.
func TestFundingSchedule8hAtAnchorZero(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultFunding = FundingSchedule{IntervalS: 28800, AnchorS: 0}
	sink := fundRun(t, cfg, []string{"A"}, 28798e9, 28810e9)
	for _, tc := range []struct {
		T                int64
		interval, settle int64
	}{
		{28799, 28800, 1},
		{28800, 28800, 28800}, // settlement second: wraps to the interval
		{28810, 28800, 28790},
	} {
		r := sink.msAt(t, tc.T, "A")
		eqI64(t, fmt.Sprintf("funding_interval_s@%d", tc.T), r.FundingIntervalS, tc.interval)
		eqI64(t, fmt.Sprintf("time_to_settle_s@%d", tc.T), r.TimeToSettleS, tc.settle)
	}
}

// TestFundingSchedule4hAtAnchor3600 (C.7b): schedule 14400/3600. Settlements
// at T = 3600, 18000, 32400, ...
func TestFundingSchedule4hAtAnchor3600(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultFunding = FundingSchedule{IntervalS: 14400, AnchorS: 3600}
	sink := fundRun(t, cfg, []string{"A"}, 0, 17995e9)
	for _, tc := range []struct {
		T                int64
		interval, settle int64
	}{
		{0, 14400, 3600},     // one hour before the 3600 settlement
		{3599, 14400, 1},     // one second before it
		{3600, 14400, 14400}, // settlement second
		{17995, 14400, 5},    // five seconds before the 18000 settlement
	} {
		r := sink.msAt(t, tc.T, "A")
		eqI64(t, fmt.Sprintf("funding_interval_s@%d", tc.T), r.FundingIntervalS, tc.interval)
		eqI64(t, fmt.Sprintf("time_to_settle_s@%d", tc.T), r.TimeToSettleS, tc.settle)
	}
}

// TestFundingPerInstrumentOverride (C.7c): Funding["B"] = {} means no
// schedule for B (both columns null) while A keeps the default hourly one.
func TestFundingPerInstrumentOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Funding = map[string]FundingSchedule{"B": {}}
	sink := fundRun(t, cfg, []string{"A", "B"}, 100e9, 100e9)
	rA := sink.msAt(t, 100, "A")
	eqI64(t, "funding_interval_s A", rA.FundingIntervalS, 3600)
	eqI64(t, "time_to_settle_s A", rA.TimeToSettleS, 3500) // 100 s past the hour edge
	rB := sink.msAt(t, 100, "B")
	nullI64(t, "funding_interval_s B", rB.FundingIntervalS)
	nullI64(t, "time_to_settle_s B", rB.TimeToSettleS)
}

// TestNewRejectsBadFundingConfig (C.7d): New refuses a negative interval, an
// anchor outside [0, interval), and an override for an instrument outside the
// universe.
func TestNewRejectsBadFundingConfig(t *testing.T) {
	bad := DefaultConfig()
	bad.DefaultFunding = FundingSchedule{IntervalS: -1, AnchorS: 0}
	badAnchor := DefaultConfig()
	badAnchor.DefaultFunding = FundingSchedule{IntervalS: 3600, AnchorS: 3600}
	badKey := DefaultConfig()
	badKey.Funding = map[string]FundingSchedule{"Z": {IntervalS: 3600, AnchorS: 0}}
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"negative interval", bad},
		{"anchor >= interval", badAnchor},
		{"override outside the universe", badKey},
	} {
		if _, err := New(tc.cfg, []string{"A"}, &recSink{}); err == nil {
			t.Errorf("New accepted %s", tc.name)
		}
	}
	// The sane shape must still be accepted.
	if _, err := New(DefaultConfig(), []string{"A"}, &recSink{}); err != nil {
		t.Fatalf("New(DefaultConfig()) = %v, want nil", err)
	}
}

// TestCtxPresenceFlags (C.7e): a ctx number whose Have bit is clear reads as
// NULL downstream, never as 0, including the derived columns.
func TestCtxPresenceFlags(t *testing.T) {
	t.Run("missing funding -> funding_rate null, mark_px present", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
			t.Fatal(err)
		}
		ctx := mkCtx("A", 100.25, 100, 1000)
		ctx.Have &^= feed.CtxFunding
		if err := g.OnAssetCtx(100e9, ctx); err != nil {
			t.Fatal(err)
		}
		g.Flush(100e9)
		r := sink.msAt(t, 100, "A")
		nullF64(t, "funding_rate", r.FundingRate)
		eqF64(t, "mark_px", r.MarkPx, 100.25)
	})

	t.Run("missing mark_px", func(t *testing.T) {
		sink := &recSink{}
		g, err := New(DefaultConfig(), []string{"A"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.OnBBO(100e9, goodBBO("A", 100000)); err != nil {
			t.Fatal(err)
		}
		ctx := mkCtx("A", 100.25, 100, 1000)
		ctx.Have &^= feed.CtxMarkPx
		if err := g.OnAssetCtx(100e9, ctx); err != nil {
			t.Fatal(err)
		}
		g.Flush(100e9)
		r := sink.msAt(t, 100, "A")
		nullF64(t, "mark_px", r.MarkPx)
		nullF64(t, "open_interest_usd", r.OpenInterestUSD)
		nullF64(t, "mark_oracle_bps", r.MarkOracleBps)
		if !r.MidOracleBps.Ok {
			t.Error("mid_oracle_bps must still be set (oracle present, mid valid)")
		}
	})
}
