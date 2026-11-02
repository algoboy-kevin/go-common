package grid

// Provisional funding benchmarks (not gated): BenchmarkFundingClose closes
// hourly periods across a 325-instrument universe with a resolved settled
// source; BenchmarkGridSecondFunding is BenchmarkGridSecond's traffic with
// funding enabled (BenchmarkGridSecond itself stays the funding-disabled
// baseline).

import (
	"fmt"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
)

// BenchmarkFundingClose: 325 instruments, hourly schedule (3600, 0). One
// iteration is one hour of ctx traffic (6 frames per instrument) and the
// close of one hourly period per instrument, including settled resolution
// and row emission through a discard funding sink.
func BenchmarkFundingClose(b *testing.B) {
	universe := make([]string, 325)
	for i := range universe {
		universe[i] = fmt.Sprintf("inst%03d", i)
	}
	g, err := New(DefaultConfig(), universe, discardSink{}) // hourly (3600, 0)
	if err != nil {
		b.Fatal(err)
	}
	g.EnableFunding(discardFundSink{}, funcSettled(
		func(string, int64) (float64, SettledState) { return 0.5, SettledFound }))
	s := newSynth(0)
	rx := int64(100e9)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i == 1 {
			// Exclude one-time setup (universe, per-instrument funding
			// state, queue ring) from the steady-state numbers.
			b.ResetTimer()
		}
		secStart := rx
		for j := 0; j < 6; j++ { // one ctx frame per instrument every 10 min
			for k := 0; k < len(universe); k++ {
				at := secStart + int64(j)*600e9 + int64(k)
				s.ctx.Coin = universe[k]
				_ = g.OnAssetCtx(at, &s.ctx)
			}
		}
		rx = secStart + 3600e9 // the next iteration's first frame closes the period
	}
}

// BenchmarkGridSecondFunding is BenchmarkGridSecond (325 instruments, one
// second of the capture traffic mix per iteration) with funding enabled, to
// price the funding bookkeeping on the hot path.
func BenchmarkGridSecondFunding(b *testing.B) {
	universe := make([]string, 325)
	for i := range universe {
		universe[i] = fmt.Sprintf("inst%03d", i)
	}
	g, err := New(DefaultConfig(), universe, discardSink{})
	if err != nil {
		b.Fatal(err)
	}
	g.EnableFunding(discardFundSink{}, funcSettled(
		func(string, int64) (float64, SettledState) { return 0.5, SettledFound }))
	s := newSynth(3)
	rx := int64(100e9)
	tid := int64(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i == 1 {
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
