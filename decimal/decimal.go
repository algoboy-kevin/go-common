// Package decimal formats float64 values as plain decimal strings for order
// prices and sizes: fixed decimals, at most N significant figures with a cap on
// decimals, and rounding up to a step. Output never uses an exponent. Non-finite
// input is passed through unchanged so the caller can reject it.
package decimal

import (
	"math"
	"strconv"
	"strings"
)

// Fixed returns v rounded to exactly decimals digits after the point.
func Fixed(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// Sig returns v with at most sig significant figures and at most maxDecimals
// digits after the point. Trailing zeros are removed, so 80606.4 with sig=5 is
// "80606" and 2.34567 with sig=5, maxDecimals=1 is "2.3". A zero input returns
// "0". sig below 1 is treated as 1.
func Sig(v float64, sig, maxDecimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	if v == 0 {
		return "0"
	}
	if sig < 1 {
		sig = 1
	}
	if maxDecimals < 0 {
		maxDecimals = 0
	}
	mag := int(math.Floor(math.Log10(math.Abs(v))))
	dec := min(max(sig-1-mag, 0), maxDecimals)
	return trimZeros(strconv.FormatFloat(v, 'f', dec, 64))
}

// CeilStep returns v rounded up to a multiple of 10^-decimals, formatted with
// exactly decimals digits. A tolerance of 1e-9 steps stops float noise (for
// example 13.0000000000001) from adding an extra step.
func CeilStep(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	step := math.Pow10(-decimals)
	n := math.Ceil(v/step - 1e-9)
	return Fixed(n*step, decimals)
}

func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
