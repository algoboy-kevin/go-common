package grid

import (
	"github.com/algoboy-kevin/go-common/market/rowsink"
	"testing"
)

func TestMarkOverflowBoundariesAndFunding(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultFunding = FundingSchedule{IntervalS: 2}
	g, s, fs := newFundGrid(t, cfg, []string{"A", "B"}, funcSettled(func(string, int64) (float64, SettledState) { return 0, SettledPending }))
	if err := g.Start(1e9); err != nil {
		t.Fatal(err)
	}
	g.MarkOverflow("missing", 0, 9e9)
	g.MarkOverflow("A", 2e9, 2e9)
	g.MarkOverflow("A", 3e9+1, 4e9)
	g.Flush(6e9)
	// Queued rows must also be marked; emitted rows are immutable.
	g.MarkOverflow("B", 4e9, 4e9)
	g.DrainFunding()
	for _, r := range s.ms {
		want := r.Instrument == "A" && (r.TsS == 2 || r.TsS == 4)
		if r.EngineOverflow != want || (r.Quality&rowsink.QOverflow != 0) != want {
			t.Fatalf("overflow @ %s/%d: %+v", r.Instrument, r.TsS, r)
		}
	}
	for _, r := range fs.rows {
		want := (r.Instrument == "A" && (r.TsS == 2 || r.TsS == 4)) || (r.Instrument == "B" && r.TsS == 4)
		if (r.SourceFlags&rowsink.FundFlagEngineOverflow != 0) != want {
			t.Fatalf("funding overflow: %+v", r)
		}
	}
}

func TestOverflowBoundedMergeAndAlloc(t *testing.T) {
	g, err := New(DefaultConfig(), []string{"A"}, discardSink{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []int64{1e9, 3e9, 5e9, 7e9, 9e9} {
		g.MarkOverflow("A", ns, ns)
	}
	var r rowsink.MarketState
	for _, tc := range []struct {
		sec  int64
		want bool
	}{{1, true}, {2, true}, {3, true}, {4, false}, {5, true}, {6, false}, {7, true}, {8, false}, {9, true}, {10, false}} {
		g.fillMarketState(&r, &g.insts[0], tc.sec)
		if r.EngineOverflow != tc.want {
			t.Fatalf("second %d: %v want %v", tc.sec, r.EngineOverflow, tc.want)
		}
	}
	if a := testing.AllocsPerRun(100, func() { g.MarkOverflow("A", 11e9, 12e9) }); a != 0 {
		t.Fatalf("allocs %v", a)
	}
}
