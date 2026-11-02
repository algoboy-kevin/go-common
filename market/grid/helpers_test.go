package grid

import (
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
	"github.com/algoboy-kevin/go-common/market/rowsink"
)

// funcSettled adapts a function to SettledSource (controllable fake).
type funcSettled func(instrument string, settleS int64) (float64, SettledState)

func (f funcSettled) Settled(instrument string, settleS int64) (float64, SettledState) {
	return f(instrument, settleS)
}

// fundRecSink records copied funding rows for assertions.
type fundRecSink struct {
	rows []rowsink.FundingSettlement
}

func (s *fundRecSink) FundingSettlement(r *rowsink.FundingSettlement) {
	s.rows = append(s.rows, *r)
}

// mkFundingCtx builds a ctx frame with every number present.
func mkFundingCtx(coin string, funding, premium, mark, oracle, oi float64) *feed.AssetCtx {
	return &feed.AssetCtx{
		Coin: coin, Have: feed.CtxAll,
		Funding: funding, Premium: premium,
		MarkPx: mark, OraclePx: oracle, OpenInterest: oi,
	}
}

// newFundGrid builds a grid with funding enabled over universe and a fake
// settled source.
func newFundGrid(t *testing.T, cfg Config, universe []string, src SettledSource) (*Grid, *recSink, *fundRecSink) {
	t.Helper()
	ms := &recSink{}
	fs := &fundRecSink{}
	g, err := New(cfg, universe, ms)
	if err != nil {
		t.Fatal(err)
	}
	g.EnableFunding(fs, src)
	return g, ms, fs
}
