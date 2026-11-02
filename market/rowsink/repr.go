package rowsink

import (
	"math"
	"strconv"
)

// AppendFloat appends the Python 3 repr(float) spelling of f to dst and
// returns the extended slice. It allocates nothing when dst has capacity:
// the intermediate shortest-round-trip digits live on the stack.
//
// Rules (CPython's repr, rule R15 "shortest round-trip form"):
//   - the digits are the shortest decimal string that round-trips to f;
//   - with value = 0.d1..dn * 10^decpt, scientific notation is used when
//     decpt <= -4 or decpt > 16 ("d1[.d2..dn]e±XX", exponent with an
//     explicit sign and at least two digits), otherwise fixed notation;
//   - fixed notation always has a decimal point ("100.0", "0.0001",
//     "-0.0"); negative zero keeps its sign ("-0.0");
//   - NaN prints "nan", infinities "inf" / "-inf" (Python spellings).
func AppendFloat(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, "nan"...)
	case math.IsInf(f, 1):
		return append(dst, "inf"...)
	case math.IsInf(f, -1):
		return append(dst, "-inf"...)
	}
	// Shortest round-trip digits in scientific form: [-]d[.ddd]e±dd.
	var scratch [32]byte
	s := strconv.AppendFloat(scratch[:0], f, 'e', -1, 64)

	neg := s[0] == '-'
	// Find the 'e', compact the mantissa digits over the front of s.
	e := 0
	for s[e] != 'e' {
		e++
	}
	n := 0
	for _, c := range s[:e] {
		if c != '.' && c != '-' {
			s[n] = c
			n++
		}
	}
	digits := s[:n]

	// Decimal exponent: value = 0.digits * 10^decpt, decpt = exp10+1.
	j := e + 1
	expNeg := s[j] == '-'
	if s[j] == '-' || s[j] == '+' {
		j++
	}
	exp10 := 0
	for ; j < len(s); j++ {
		exp10 = exp10*10 + int(s[j]-'0')
	}
	if expNeg {
		exp10 = -exp10
	}
	decpt := exp10 + 1

	if neg {
		dst = append(dst, '-')
	}
	if decpt <= -4 || decpt > 16 {
		// Scientific: d1[.d2..dn]e±XX, exponent at least two digits.
		dst = append(dst, digits[0])
		if n > 1 {
			dst = append(dst, '.')
			dst = append(dst, digits[1:]...)
		}
		dst = append(dst, 'e')
		exp := decpt - 1
		if exp < 0 {
			dst = append(dst, '-')
			exp = -exp
		} else {
			dst = append(dst, '+')
		}
		if exp < 10 {
			dst = append(dst, '0')
		}
		dst = strconv.AppendInt(dst, int64(exp), 10)
		return dst
	}
	// Fixed notation, always with a decimal point.
	switch {
	case decpt <= 0:
		dst = append(dst, '0', '.')
		for i := 0; i < -decpt; i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, digits...)
	case decpt < n:
		dst = append(dst, digits[:decpt]...)
		dst = append(dst, '.')
		dst = append(dst, digits[decpt:]...)
	default: // decpt >= n: integer part, then ".0"
		dst = append(dst, digits...)
		for i := n; i < decpt; i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, '.', '0')
	}
	return dst
}

// FormatFloat returns the Python 3 repr(float) spelling of f (see AppendFloat).
func FormatFloat(f float64) string {
	return string(AppendFloat(nil, f))
}
