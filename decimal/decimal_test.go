package decimal

import (
	"math"
	"testing"
)

func TestFixed(t *testing.T) {
	tests := []struct {
		name     string
		v        float64
		decimals int
		want     string
	}{
		{"keeps zeros", 1, 3, "1.000"},
		{"small size", 0.00014, 5, "0.00014"},
		{"negative", -1.25, 2, "-1.25"},
		{"negative decimals clamp to zero", 7.9, -2, "8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Fixed(tc.v, tc.decimals); got != tc.want {
				t.Fatalf("Fixed(%v, %d) = %q, want %q", tc.v, tc.decimals, got, tc.want)
			}
		})
	}
}

func TestSig(t *testing.T) {
	tests := []struct {
		name        string
		v           float64
		sig         int
		maxDecimals int
		want        string
	}{
		{"five sig, whole price", 80606.4, 5, 1, "80606"},
		{"decimal cap wins over sig", 2.34567, 5, 1, "2.3"},
		{"sig wins over large cap", 2.34567, 5, 6, "2.3457"},
		{"sub-one price", 0.12345678, 5, 6, "0.12346"},
		{"tiny price, cap leaves room for five sig", 0.0000123456, 5, 9, "0.000012346"},
		{"tiny price, cap binds before sig", 0.0000123456, 5, 8, "0.00001235"},
		{"rounds up into next magnitude", 99999.9, 5, 6, "100000"},
		{"trims trailing zeros", 1.5, 5, 6, "1.5"},
		{"zero", 0, 5, 6, "0"},
		{"negative", -1234.5678, 5, 6, "-1234.6"},
		{"half magnitude", 0.5, 1, 6, "0.5"},
		{"nan passes through", math.NaN(), 5, 6, "NaN"},
		{"inf passes through", math.Inf(1), 5, 6, "+Inf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sig(tc.v, tc.sig, tc.maxDecimals); got != tc.want {
				t.Fatalf("Sig(%v, %d, %d) = %q, want %q", tc.v, tc.sig, tc.maxDecimals, got, tc.want)
			}
		})
	}
}

func TestCeilStep(t *testing.T) {
	tests := []struct {
		name     string
		v        float64
		decimals int
		want     string
	}{
		{"rounds up to next step", 0.0001375, 5, "0.00014"},
		{"exact step unchanged", 13, 0, "13"},
		{"float noise above step does not add one", 13.0000000000001, 0, "13"},
		{"just above step adds one", 13.2, 0, "14"},
		{"float sum that equals a step", 0.1 + 0.2, 1, "0.3"},
		{"negative rounds up toward positive infinity", -1.2, 0, "-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CeilStep(tc.v, tc.decimals); got != tc.want {
				t.Fatalf("CeilStep(%v, %d) = %q, want %q", tc.v, tc.decimals, got, tc.want)
			}
		})
	}
}
