package grid

// Provisional measurements (not gated): steady-state zero allocations,
// per-instrument memory including the lazy R14 tid windows, and a
// synthetic one-second benchmark.

import (
	"fmt"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

type discardSink struct{}

func (discardSink) MarketState(*rowsink.MarketState) {}
func (discardSink) TradeFlow(*rowsink.TradeFlow)     {}

// synth holds reusable frame templates so the loops below allocate nothing.
type synth struct {
	bbo    feed.BBO
	l2     feed.L2Book
	ctx    feed.AssetCtx
	prints []feed.Trade
}

func newSynth(nPrints int) *synth {
	s := &synth{
		bbo: feed.BBO{HasBid: true, HasAsk: true,
			Bid: feed.Level{Px: 100, Sz: 2, N: 1}, Ask: feed.Level{Px: 100.25, Sz: 4, N: 2}},
		ctx: feed.AssetCtx{Have: feed.CtxAll, MarkPx: 100.25, OraclePx: 100, OpenInterest: 1000, DayNtlVlm: 5000,
			ImpactBid: 100, ImpactAsk: 100.25, HasImpact: true, Funding: 1.25e-5, Premium: 1e-4},
		prints: make([]feed.Trade, nPrints),
	}
	s.l2.NBids, s.l2.NAsks = 3, 3
	s.l2.Bids[0] = feed.Level{Px: 100, Sz: 2, N: 1}
	s.l2.Bids[1] = feed.Level{Px: 99.75, Sz: 4, N: 2}
	s.l2.Bids[2] = feed.Level{Px: 99.5, Sz: 8, N: 3}
	s.l2.Asks[0] = feed.Level{Px: 100.25, Sz: 2, N: 1}
	s.l2.Asks[1] = feed.Level{Px: 100.5, Sz: 4, N: 2}
	s.l2.Asks[2] = feed.Level{Px: 100.75, Sz: 8, N: 3}
	return s
}

// TestAllocZeroAfterWarmup requires OnBBO/OnL2Book/OnTrades/OnAssetCtx plus
// the second-boundary emission to allocate exactly zero per call in steady
// state (including tid-window eviction and backward-shift deletion).
func TestAllocZeroAfterWarmup(t *testing.T) {
	universe := []string{"A", "B", "C", "D"}
	g, err := New(DefaultConfig(), universe, discardSink{})
	if err != nil {
		t.Fatal(err)
	}
	s := newSynth(1)
	rx := int64(100e9)
	tid := int64(1)
	step := func() {
		// 250 ms of traffic per instrument: a boundary is crossed each run.
		for k := 0; k < len(universe); k++ {
			coin := universe[k]
			s.bbo.Coin, s.bbo.TimeMs = coin, rx/1e6
			_ = g.OnBBO(rx, &s.bbo)
			s.l2.Coin, s.l2.TimeMs = coin, rx/1e6
			_ = g.OnL2Book(rx, &s.l2)
			s.ctx.Coin = coin
			_ = g.OnAssetCtx(rx, &s.ctx)
			s.prints[0] = feed.Trade{Coin: coin, Side: 'B', Px: 100, Sz: 1, TimeMs: rx / 1e6, Tid: tid}
			tid++
			_ = g.OnTrades(rx, s.prints)
			rx += 250e6
		}
	}
	for i := 0; i < 16; i++ { // warm-up: lazy structures and emissions
		step()
	}
	// 6000 runs x 4 prints/instrument pushes every window past TidWindow so
	// eviction + delete are inside the measured region too.
	allocs := testing.AllocsPerRun(6000, step)
	if allocs != 0 {
		t.Fatalf("steady-state On*+emission allocated %v per run, want 0", allocs)
	}
}

// BenchmarkGridSecond: a synthetic 325-instrument universe, one second of
// traffic per iteration (6 bbo, 2 l2, 3 trades frames with 3 prints, 1 ctx
// per instrument; roughly the capture mix of 6/1.85/2.5/1).
func BenchmarkGridSecond(b *testing.B) {
	universe := make([]string, 325)
	for i := range universe {
		universe[i] = fmt.Sprintf("inst%03d", i)
	}
	g, err := New(DefaultConfig(), universe, discardSink{})
	if err != nil {
		b.Fatal(err)
	}
	s := newSynth(3)
	rx := int64(100e9)
	tid := int64(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i == 1 {
			// Exclude the one-time lazy per-instrument init (rows and R14
			// tid windows) from the steady-state numbers; TestMemoryFootprint
			// reports that cost.
			b.ResetTimer()
		}
		secStart := rx
		for k := 0; k < len(universe); k++ {
			coin := universe[k]
			base := secStart + int64(k)*2e6
			for j := 0; j < 6; j++ {
				at := base + int64(j)*1e5
				s.bbo.Coin, s.bbo.TimeMs = coin, at/1e6
				_ = g.OnBBO(at, &s.bbo)
			}
			for j := 0; j < 2; j++ {
				at := base + 6e5 + int64(j)*1e5
				s.l2.Coin, s.l2.TimeMs = coin, at/1e6
				_ = g.OnL2Book(at, &s.l2)
			}
			for j := range s.prints {
				s.prints[j] = feed.Trade{Coin: coin, Side: 'B', Px: 100, Sz: 1,
					TimeMs: (base + 8e5) / 1e6, Tid: tid}
				tid++
			}
			_ = g.OnTrades(base+8e5, s.prints)
			s.ctx.Coin = coin
			_ = g.OnAssetCtx(base+9e5, &s.ctx)
		}
		rx = secStart + 1e9
	}
}
