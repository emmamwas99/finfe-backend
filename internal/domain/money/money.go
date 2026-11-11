// Package money holds integer money helpers. Amounts are int64 minor units (cents);
// rates are basis points. Floating point is only used for display ratios at the very end.
package money

import (
	"fmt"
	"regexp"
)

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidCurrency reports whether c looks like an ISO-4217 code.
func ValidCurrency(c string) bool { return currencyRe.MatchString(c) }

// DivRound divides with round-half-away-from-zero (banker's-style bias avoided for money).
func DivRound(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	if (a < 0) != (b < 0) {
		return -((abs(a) + abs(b)/2) / abs(b))
	}
	return (abs(a) + abs(b)/2) / abs(b)
}

// MulBp multiplies an amount by a basis-point rate, rounded (e.g. 8000 bp = 80%).
func MulBp(amount int64, bp int64) int64 { return DivRound(amount*bp, 10_000) }

// Ratio returns a/b as a float for display (0 when b is 0). Never feed it back into money maths.
func Ratio(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Format renders minor units as "KES 1,250.50" (for logs, emails and tests).
func Format(minor int64, currency string) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	whole, frac := minor/100, minor%100
	s := fmt.Sprintf("%d", whole)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return fmt.Sprintf("%s%s %s.%02d", sign, currency, s, frac)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Display renders an amount for human-readable insight text: whole units for KES
// ("KSh 85,000", as Kenyans write it), two decimals for other currencies ("USD 1,250.50").
func Display(minor int64, currency string) string {
	if currency == "KES" || currency == "" {
		whole := DivRound(minor, 100)
		s := Format(whole*100, "KSh")
		return s[:len(s)-3] // drop ".00"
	}
	return Format(minor, currency)
}
