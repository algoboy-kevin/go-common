package rowsink

import (
	"strings"
	"testing"
)

// TestCompareNullVsZero guards: never fabricate — null and 0.0 are
// different values and must be reported as different.
func TestCompareNullVsZero(t *testing.T) {
	nullRow := MarketState{BidPx: F64{}}
	zeroRow := MarketState{BidPx: Some(0)}
	diffs := CompareMarketState([]MarketState{nullRow}, []MarketState{zeroRow})
	if len(diffs) != 1 || diffs[0].Column != "bid_px" {
		t.Fatalf("null vs 0.0 gave %v, want one bid_px diff", diffs)
	}
	// And the reverse direction.
	diffs = CompareMarketState([]MarketState{zeroRow}, []MarketState{nullRow})
	if len(diffs) != 1 {
		t.Fatalf("0.0 vs null gave %v, want one diff", diffs)
	}
	// Both null: equal.
	diffs = CompareMarketState([]MarketState{{BidPx: F64{}}}, []MarketState{{BidPx: F64{}}})
	if len(diffs) != 0 {
		t.Fatalf("null vs null gave %v, want no diffs", diffs)
	}
	// Nullable bool and int too.
	bdiffs := CompareMarketState([]MarketState{{L2ReachBid: F64{}}}, []MarketState{{L2ReachBid: Some(0)}})
	if len(bdiffs) != 1 {
		t.Fatalf("null vs false gave %v, want one diff", bdiffs)
	}
}

// TestCompareRowCountMismatch guards: unequal lengths must stop with the
// single "<row count>" Diff, not panic on an out-of-range index.
func TestCompareRowCountMismatch(t *testing.T) {
	a := []MarketState{{}, {}, {}}
	b := []MarketState{{}, {}}
	diffs := CompareMarketState(a, b)
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	d := diffs[0]
	if d.Row != -1 || d.Column != "<row count>" || d.A != "3" || d.B != "2" {
		t.Errorf("got %+v, want {-1 <row count> 3 2}", d)
	}
	if !strings.Contains(d.String(), "<row count>") {
		t.Errorf("Diff.String() = %q must name <row count>", d.String())
	}
	tdiffs := CompareTradeFlow([]TradeFlow{{}}, []TradeFlow{{}, {}})
	if len(tdiffs) != 1 || tdiffs[0].Column != "<row count>" {
		t.Errorf("trade flow row-count mismatch gave %v", tdiffs)
	}
}

// TestFloatClose pins the comparison rule: relative 1e-9, absolute 1e-12.
func TestFloatClose(t *testing.T) {
	cases := []struct {
		name string
		x, y float64
		want bool
	}{
		{"rel inside", 1.0, 1.0 + 1e-10, true},
		{"rel outside", 1.0, 1.0 + 1e-6, false},
		{"abs inside", 0, 1e-13, true},
		{"abs outside", 0, 1e-11, false},
		{"symmetric inside", 1.0 + 1e-10, 1.0, true},
		{"symmetric outside", 1.0 + 1e-6, 1.0, false},
		{"large rel inside", 1e12, 1e12 * (1 + 1e-10), true},
		{"large rel outside", 1e12, 1e12 * (1 + 1e-7), false},
	}
	for _, c := range cases {
		if got := floatClose(c.x, c.y); got != c.want {
			t.Errorf("%s: floatClose(%v, %v) = %v, want %v", c.name, c.x, c.y, got, c.want)
		}
	}
}
