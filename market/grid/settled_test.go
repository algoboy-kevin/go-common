package grid

// Rule R18 window tests for NewMapSettled: the entry for period S is the
// first with S*1000 <= time < S*1000+60000 (the window is half-open).

import "testing"

func TestMapSettledWindow(t *testing.T) {
	src := NewMapSettled(map[string][]SettledPoint{
		// Two entries inside period 3600's window: the first wins.
		"A": {
			{TimeMs: 3600000, Rate: 0.5},  // exactly S*1000: in (lower edge inclusive)
			{TimeMs: 3600001, Rate: 0.75}, // in, but not first
		},
		// Only the upper edge: +60000 is OUT of period 3600's window.
		"U": {{TimeMs: 3660000, Rate: 0.25}},
		// Only the last valid instant: +59999 is IN.
		"L": {{TimeMs: 3659999, Rate: 0.125}},
		"B": {}, // supplied but with no entries: SettledMissing
	})
	for _, tc := range []struct {
		instrument string
		settleS    int64
		wantSt     SettledState
		wantRate   float64
	}{
		{"A", 3600, SettledFound, 0.5},    // first of the two matches wins
		{"U", 3600, SettledMissing, 0},    // +60000 is not < +60000
		{"U", 3601, SettledFound, 0.25},   // ...but it stamps the NEXT period
		{"L", 3600, SettledFound, 0.125},  // +59999 is the last instant in
		{"A", 3601, SettledMissing, 0},    // no entry in [3601000, 3661000)
		{"B", 3600, SettledMissing, 0},    // present, no entry
		{"C", 3600, SettledNotFetched, 0}, // absent from the map
	} {
		rate, st := src.Settled(tc.instrument, tc.settleS)
		if st != tc.wantSt {
			t.Errorf("Settled(%q, %d) state = %d, want %d", tc.instrument, tc.settleS, st, tc.wantSt)
			continue
		}
		if st == SettledFound && rate != tc.wantRate {
			t.Errorf("Settled(%q, %d) rate = %v, want %v", tc.instrument, tc.settleS, rate, tc.wantRate)
		}
	}
}
