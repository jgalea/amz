package amazon

import (
	"regexp"
	"strconv"
	"strings"
)

var amountDigitsRe = regexp.MustCompile(`[\d.,]+`)

// ParseAmount turns a storefront amount ("+€99.99", "-€213.78",
// "45,98 €", "1.234,56 €", "$1,234.56") into a signed number. The
// second result is false when there is no number in the string.
func ParseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	negative := strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−")
	digits := amountDigitsRe.FindString(s)
	if digits == "" {
		return 0, false
	}
	digits = strings.Trim(digits, ".,")
	lastDot, lastComma := strings.LastIndex(digits, "."), strings.LastIndex(digits, ",")
	var whole, frac string
	switch {
	case lastDot >= 0 && lastComma >= 0:
		// Both present: the later one is the decimal separator.
		sep := lastDot
		if lastComma > lastDot {
			sep = lastComma
		}
		whole, frac = digits[:sep], digits[sep+1:]
	case lastDot >= 0 || lastComma >= 0:
		sep := lastDot
		if sep < 0 {
			sep = lastComma
		}
		// One separator, once, with two digits after it is a decimal
		// point; anything else groups thousands.
		if strings.Count(digits, digits[sep:sep+1]) == 1 && len(digits)-sep-1 == 2 {
			whole, frac = digits[:sep], digits[sep+1:]
		} else {
			whole = digits
		}
	default:
		whole = digits
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	n, err := strconv.ParseFloat(whole+"."+padFrac(frac), 64)
	if err != nil {
		return 0, false
	}
	if negative {
		n = -n
	}
	return n, true
}

func padFrac(f string) string {
	if f == "" {
		return "0"
	}
	return f
}
