package grid

// Rule R20 engine-behaviour tests: the settled-value queue (head-of-line,
// deadline, no re-emission, drain) and the zero-allocation steady state.

import (
	"testing"

	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// TestFundingPendingHeadBlocksQueue (failure mode: rows leaking out of
// order behind an unresolved head). A resolves late, B is ready at once;
// B must wait behind A and both come out in (ts_s, universe) order.
func TestFundingPendingHeadBlocksQueue(t *testing.T) {
	aFound := false
	src := funcSettled(func(instrument string, _ int64) (float64, SettledState) {
		if instrument == "A" {
			if !aFound {
				return 0, SettledPending
			}
			return 0.5, SettledFound
		}
		return 0.25, SettledFound
	})
	g, _, fs := newFundGrid(t, DefaultConfig(), []string{"A", "B"}, src)
	for _, coin := range []string{"A", "B"} {
		if err := g.OnAssetCtx(100e9, mkFundingCtx(coin, 1, 0.5, 100, 101, 10)); err != nil {
			t.Fatal(err)
		}
	}
	// This frame closes both periods (S=3600) and drains the queue.
	if err := g.OnAssetCtx(3600e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 0 {
		t.Fatalf("pending head must block the queue, got %d rows", len(fs.rows))
	}
	aFound = true
	if err := g.OnAssetCtx(3600e9+2, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 2 {
		t.Fatalf("got %d rows, want 2 after A resolved", len(fs.rows))
	}
	if fs.rows[0].Instrument != "A" || fs.rows[1].Instrument != "B" {
		t.Fatalf("emission order = %s,%s, want A,B", fs.rows[0].Instrument, fs.rows[1].Instrument)
	}
	eqF64(t, "funding_settled A", fs.rows[0].FundingSettled, 0.5)
	eqF64(t, "funding_settled B", fs.rows[1].FundingSettled, 0.25)
}

// TestFundingPendingDeadlineForcesFlag1 (failure mode: a dead settled
// source blocking the stream forever). Once frame time passes
// S*1e9 + settled_wait_s the head emits with SETTLED_NOT_FETCHED and the
// queue continues.
func TestFundingPendingDeadlineForcesFlag1(t *testing.T) {
	src := funcSettled(func(instrument string, _ int64) (float64, SettledState) {
		if instrument == "A" {
			return 0, SettledPending // never resolves
		}
		return 0.25, SettledFound
	})
	g, _, fs := newFundGrid(t, DefaultConfig(), []string{"A", "B"}, src)
	for _, coin := range []string{"A", "B"} {
		if err := g.OnAssetCtx(100e9, mkFundingCtx(coin, 1, 0.5, 100, 101, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.OnAssetCtx(3600e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 0 {
		t.Fatalf("got %d rows before the deadline, want 0", len(fs.rows))
	}
	// Deadline is S*1e9 + 600e9 = 4200e9 (DefaultConfig.SettledWaitS).
	if err := g.OnAssetCtx(4200e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 2 {
		t.Fatalf("got %d rows past the deadline, want 2 (the queue continues)", len(fs.rows))
	}
	a, b := &fs.rows[0], &fs.rows[1]
	if a.Instrument != "A" || b.Instrument != "B" {
		t.Fatalf("emission order = %s,%s, want A,B", a.Instrument, b.Instrument)
	}
	if a.FundingSettled.Ok {
		t.Errorf("A funding_settled = %+v, want null (forced)", a.FundingSettled)
	}
	if a.SourceFlags&rowsink.FundFlagSettledNotFetched == 0 {
		t.Errorf("A flags = %d, want SETTLED_NOT_FETCHED set", a.SourceFlags)
	}
	eqF64(t, "funding_settled B", b.FundingSettled, 0.25)
	if b.SourceFlags&rowsink.FundFlagSettledNotFetched != 0 {
		t.Errorf("B flags = %d, want SETTLED_NOT_FETCHED clear", b.SourceFlags)
	}
}

// TestFundingFoundAfterWaitUsesValue (failure mode: discarding a value
// that arrives before the deadline).
func TestFundingFoundAfterWaitUsesValue(t *testing.T) {
	pending := true
	src := funcSettled(func(string, int64) (float64, SettledState) {
		if pending {
			return 0, SettledPending
		}
		return 0.125, SettledFound
	})
	g, _, fs := newFundGrid(t, DefaultConfig(), []string{"A"}, src)
	if err := g.OnAssetCtx(100e9, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnAssetCtx(3600e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 0 {
		t.Fatalf("got %d rows while pending, want 0", len(fs.rows))
	}
	pending = false // value arrives, well before the deadline
	if err := g.OnAssetCtx(3700e9, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(fs.rows))
	}
	r := &fs.rows[0]
	eqF64(t, "funding_settled", r.FundingSettled, 0.125)
	if r.SourceFlags&rowsink.FundFlagSettledNotFetched != 0 {
		t.Errorf("flags = %d, want SETTLED_NOT_FETCHED clear", r.SourceFlags)
	}
}

// TestFundingNoReemitAfterDeadline (failure mode: a settled value arriving
// after the row was emitted must not append a second row; output stays
// append-only).
func TestFundingNoReemitAfterDeadline(t *testing.T) {
	pending := true
	src := funcSettled(func(string, int64) (float64, SettledState) {
		if pending {
			return 0, SettledPending
		}
		return 0.9, SettledFound
	})
	g, _, fs := newFundGrid(t, DefaultConfig(), []string{"A"}, src)
	if err := g.OnAssetCtx(100e9, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnAssetCtx(3600e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	// Past the deadline: emitted once with flag 1.
	if err := g.OnAssetCtx(4200e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 1 || fs.rows[0].SourceFlags&rowsink.FundFlagSettledNotFetched == 0 {
		t.Fatalf("got %d rows %+v, want one forced row", len(fs.rows), fs.rows)
	}
	// The value becomes available: never re-emitted.
	pending = false
	if err := g.OnAssetCtx(4300e9, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	g.DrainFunding()
	if len(fs.rows) != 1 {
		t.Fatalf("got %d rows, want 1 (late values are not re-emitted)", len(fs.rows))
	}
}

// TestFundingDrainEmitsQueue (failure mode: queued rows lost at shutdown).
// DrainFunding force-emits unresolved rows with flag 1 and resolved rows
// with their value, in order.
func TestFundingDrainEmitsQueue(t *testing.T) {
	src := funcSettled(func(instrument string, _ int64) (float64, SettledState) {
		if instrument == "A" {
			return 0, SettledPending
		}
		return 0.25, SettledFound
	})
	g, _, fs := newFundGrid(t, DefaultConfig(), []string{"A", "B"}, src)
	for _, coin := range []string{"A", "B"} {
		if err := g.OnAssetCtx(100e9, mkFundingCtx(coin, 1, 0.5, 100, 101, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.OnAssetCtx(3600e9+1, mkFundingCtx("A", 1, 0.5, 100, 101, 10)); err != nil {
		t.Fatal(err)
	}
	if len(fs.rows) != 0 {
		t.Fatalf("got %d rows before drain, want 0", len(fs.rows))
	}
	g.DrainFunding()
	if len(fs.rows) != 2 {
		t.Fatalf("got %d rows after drain, want 2", len(fs.rows))
	}
	a, b := &fs.rows[0], &fs.rows[1]
	if a.Instrument != "A" || a.SourceFlags&rowsink.FundFlagSettledNotFetched == 0 {
		t.Errorf("A = %+v, want forced with SETTLED_NOT_FETCHED", a)
	}
	if b.Instrument != "B" {
		t.Errorf("B = %+v, want the resolved row after A", b)
	}
	eqF64(t, "funding_settled B", b.FundingSettled, 0.25)
	// A second drain emits nothing.
	g.DrainFunding()
	if len(fs.rows) != 2 {
		t.Fatalf("got %d rows after second drain, want 2", len(fs.rows))
	}
}

// TestNewRejectsNegativeSettledWaitS (failure mode: an unvalidatable
// deadline knob).
func TestNewRejectsNegativeSettledWaitS(t *testing.T) {
	if DefaultConfig().SettledWaitS != 600 {
		t.Errorf("DefaultConfig().SettledWaitS = %d, want 600", DefaultConfig().SettledWaitS)
	}
	cfg := DefaultConfig()
	cfg.SettledWaitS = -1
	if _, err := New(cfg, []string{"A"}, &recSink{}); err == nil {
		t.Error("New accepted SettledWaitS < 0")
	}
	cfg.SettledWaitS = 0 // allowed: every Pending head force-emits at once
	if _, err := New(cfg, []string{"A"}, &recSink{}); err != nil {
		t.Errorf("New rejected SettledWaitS = 0: %v", err)
	}
}

// discardFundSink is the allocation-free funding sink for the alloc tests
// and benchmarks.
type discardFundSink struct{}

func (discardFundSink) FundingSettlement(*rowsink.FundingSettlement) {}

// TestFundingAllocZeroCtxFrames guards: steady-state ctx accumulation
// allocates exactly zero per frame (funding enabled, no period close).
func TestFundingAllocZeroCtxFrames(t *testing.T) {
	universe := []string{"A", "B", "C", "D"}
	g, err := New(DefaultConfig(), universe, discardSink{})
	if err != nil {
		t.Fatal(err)
	}
	g.EnableFunding(discardFundSink{}, funcSettled(
		func(string, int64) (float64, SettledState) { return 0.5, SettledFound }))
	s := newSynth(0)
	rx := int64(100e9)
	step := func() {
		for k := range universe {
			s.ctx.Coin = universe[k]
			_ = g.OnAssetCtx(rx, &s.ctx)
		}
		rx += 250e6 // 250 ms per run: stays inside one 3600 s period
	}
	for i := 0; i < 16; i++ { // warm-up
		step()
	}
	if allocs := testing.AllocsPerRun(2000, step); allocs != 0 {
		t.Fatalf("steady-state ctx frames allocated %v per run, want 0", allocs)
	}
}

// TestFundingAllocZeroPeriodClose guards: a period close with a resolved
// settled source (queue push, resolution, emission) allocates exactly zero.
func TestFundingAllocZeroPeriodClose(t *testing.T) {
	universe := []string{"A", "B", "C", "D"}
	cfg := DefaultConfig()
	cfg.DefaultFunding = FundingSchedule{IntervalS: 1, AnchorS: 0} // 1 s periods
	g, err := New(cfg, universe, discardSink{})
	if err != nil {
		t.Fatal(err)
	}
	g.EnableFunding(discardFundSink{}, funcSettled(
		func(string, int64) (float64, SettledState) { return 0.5, SettledFound }))
	s := newSynth(0)
	rx := int64(100e9)
	step := func() {
		rx += 1e9 // one frame per run past a boundary: closes one period
		for k := range universe {
			s.ctx.Coin = universe[k]
			_ = g.OnAssetCtx(rx, &s.ctx)
		}
	}
	for i := 0; i < 16; i++ { // warm-up: queue ring, row reuse
		step()
	}
	if allocs := testing.AllocsPerRun(2000, step); allocs != 0 {
		t.Fatalf("period close with resolved source allocated %v per run, want 0", allocs)
	}
}
