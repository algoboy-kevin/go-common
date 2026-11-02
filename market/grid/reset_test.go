package grid

import (
	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
	"testing"
)

func TestResetInstrumentIsolatedAndAllocationFree(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultFunding = FundingSchedule{IntervalS: 4}
	g, s, fs := newFundGrid(t, cfg, []string{"A", "B"}, NewMapSettled(nil))
	for _, name := range []string{"A", "B"} {
		if err := g.OnBBO(1e9, goodBBO(name, 1000)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnAssetCtx(1e9, mkCtx(name, 10, 10, 2)); err != nil {
			t.Fatal(err)
		}
		if err := g.OnTrades(1e9, []feed.Trade{{Coin: name, Tid: 1, Side: feed.SideBuy, Px: 10, Sz: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	g.MarkOverflow("A", 2e9, 2e9)
	g.Flush(1e9)
	if err := g.OnMeta(2e9, feed.KindBBO, MetaDisconnected); err != nil {
		t.Fatal(err)
	}
	tids := g.insts[0].tids
	funding := g.insts[0].fs
	if a := testing.AllocsPerRun(100, func() { g.ResetInstrument("A") }); a != 0 {
		t.Fatalf("reset allocs %v", a)
	}
	g.ResetInstrument("unknown")
	if g.insts[0].tids != tids || g.insts[0].fs != funding {
		t.Fatal("reset discarded preallocated storage")
	}
	if err := g.OnBBO(1e9, goodBBO("A", 1000)); err != ErrOutOfOrder {
		t.Fatalf("watermark reset: %v", err)
	}
	g.Flush(2e9)
	a, b := s.msAt(t, 2, "A"), s.msAt(t, 2, "B")
	if a.BidPx.Ok || a.CtxValid || a.Valid || a.FeedGap || !a.EngineOverflow {
		t.Fatalf("reset A: %+v", a)
	}
	if !b.BidPx.Ok || !b.CtxValid || !b.FeedGap || b.EngineOverflow {
		t.Fatalf("changed B: %+v", b)
	}
	if err := g.OnTrades(3e9, []feed.Trade{{Coin: "A", Tid: 1, Side: feed.SideBuy, Px: 10, Sz: 1}}); err != nil {
		t.Fatal(err)
	}
	g.Flush(4e9)
	g.DrainFunding()
	if s.tfAt(t, 3, "A").BuyEvents != 1 {
		t.Fatal("tid window not reset")
	}
	for _, r := range fs.rows {
		if r.Instrument == "A" && (r.CtxFrames != 0 || r.SourceFlags&rowsink.FundFlagEngineOverflow == 0) {
			t.Fatalf("fund reset: %+v", r)
		}
	}
}
