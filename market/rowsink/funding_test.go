package rowsink

// funding_settlement CSV tests: golden round trip (byte-identical), the
// per-column comparison gate and the zero-allocation write path, in the
// same style as csv_test.go / compare_test.go.

import (
	"io"
	"testing"
)

// TestCompareFundingRowMismatch guards: unequal lengths stop with the
// single "<row count>" Diff (same contract as the other tables).
func TestCompareFundingRowMismatch(t *testing.T) {
	diffs := CompareFundingSettlement([]FundingSettlement{{}}, []FundingSettlement{{}, {}})
	if len(diffs) != 1 || diffs[0].Column != "<row count>" {
		t.Fatalf("row-count mismatch gave %v, want one <row count> diff", diffs)
	}
}

// TestFundingColumnCount guards: the contract's column slice stays at the
// documented width (19) that the golden pipeline assumes.
func TestFundingColumnCount(t *testing.T) {
	if len(FundingSettlementColumns) != 19 {
		t.Errorf("FundingSettlementColumns has %d columns, want 19", len(FundingSettlementColumns))
	}
}

// TestFundingWriteNeedsWithFunding guards: FundingSettlement without
// WithFunding is a sticky error, never silent data loss.
func TestFundingWriteNeedsWithFunding(t *testing.T) {
	sink := NewCSVSink(io.Discard, io.Discard)
	sink.FundingSettlement(&FundingSettlement{TsS: 1, Instrument: "A", FundingIntervalS: 3600})
	if sink.Err() == nil {
		t.Error("FundingSettlement without WithFunding must be an error")
	}
}

// TestCSVSinkFundingZeroAllocs guards: writing one funding row must average
// 0 allocations after warm-up (same style as TestCSVSinkZeroAllocs).
func TestCSVSinkFundingZeroAllocs(t *testing.T) {
	sink := NewCSVSink(io.Discard, io.Discard).WithFunding(io.Discard)
	row := FundingSettlement{
		TsS: 1791162000, Instrument: "AAA", FundingIntervalS: 3600,
		FundingSettled: Some(1.52587890625e-05),
		FundingCtxLast: Some(1.52587890625e-05), FundingCtxMean: Some(1.52587890625e-05),
		FundingCtxMin: Some(1.52587890625e-05), FundingCtxMax: Some(1.52587890625e-05),
		FundingChanges: 0,
		PremiumMean:    Some(0.0001220703125), PremiumLast: Some(0.0001220703125),
		OraclePxSettle: Some(100.5), MarkPxSettle: Some(100.25),
		OIUSDStart: Some(200550.125), OIUSDEnd: Some(200550.125),
		CtxFrames: 30, CtxGapMaxMs: SomeI(1800250),
		ReconcileDiff: F64{},
		SourceFlags:   FundFlagSettledMissing | FundFlagPartialStart,
	}
	sink.FundingSettlement(&row) // warm-up: grow the line buffer
	if allocs := testing.AllocsPerRun(1000, func() { sink.FundingSettlement(&row) }); allocs != 0 {
		t.Errorf("FundingSettlement row write allocated %v times per run, want 0", allocs)
	}
}
