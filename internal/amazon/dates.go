package amazon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var months = map[string]int{
	// en
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
	// es
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6,
	"julio": 7, "agosto": 8, "septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
	// pt
	"janeiro": 1, "fevereiro": 2, "março": 3, "maio": 5, "junho": 6,
	"julho": 7, "setembro": 9, "outubro": 10, "novembro": 11, "dezembro": 12,
	// it
	"gennaio": 1, "febbraio": 2, "aprile": 4, "maggio": 5, "giugno": 6,
	"luglio": 7, "settembre": 9, "ottobre": 10, "dicembre": 12,
	// fr
	"janvier": 1, "février": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6,
	"juillet": 7, "août": 8, "septembre": 9, "octobre": 10, "novembre": 11, "décembre": 12,
	// de
	"januar": 1, "februar": 2, "märz": 3, "juni": 6, "juli": 7, "oktober": 10, "dezember": 12,
}

var (
	dayMonthYearRe = regexp.MustCompile(`(?i)(\d{1,2})\.?\s+(?:de\s+)?([\p{L}]+)\.?,?\s+(?:de\s+)?(\d{4})`)
	monthDayYearRe = regexp.MustCompile(`(?i)([\p{L}]+)\.?\s+(\d{1,2}),?\s+(\d{4})`)
	numericDateRe  = regexp.MustCompile(`(\d{1,2})[./](\d{1,2})[./](\d{4})`)
)

// ParseDate turns the storefront's spelled-out date ("12 de septiembre
// de 2025", "September 12, 2025", "12. September 2025") into
// YYYY-MM-DD, or "" when it cannot.
func ParseDate(s string) string {
	s = strings.TrimSpace(s)
	if m := dayMonthYearRe.FindStringSubmatch(s); m != nil {
		if mon, ok := months[strings.ToLower(m[2])]; ok {
			return iso(m[3], mon, m[1])
		}
	}
	if m := monthDayYearRe.FindStringSubmatch(s); m != nil {
		if mon, ok := months[strings.ToLower(m[1])]; ok {
			return iso(m[3], mon, m[2])
		}
	}
	if m := numericDateRe.FindStringSubmatch(s); m != nil {
		mon, _ := strconv.Atoi(m[2])
		if mon >= 1 && mon <= 12 {
			return iso(m[3], mon, m[1])
		}
	}
	return ""
}

func iso(year string, month int, day string) string {
	d, _ := strconv.Atoi(day)
	if d < 1 || d > 31 {
		return ""
	}
	return fmt.Sprintf("%s-%02d-%02d", year, month, d)
}

var dayMonthRe = regexp.MustCompile(`(?i)(\d{1,2})\.?\s+(?:de\s+)?([\p{L}]+)\.?`)
var monthDayRe = regexp.MustCompile(`(?i)([\p{L}]+)\.?\s+(\d{1,2})\b`)

// ParseDateNear reads a date that may lack its year ("Delivered 14
// September", "Entregado el 3 de octubre") and places it in the year of
// refISO, or the next one when that would put it more than a month
// before the reference. With a full date in the text it behaves like
// ParseDate.
func ParseDateNear(s, refISO string) string {
	if d := ParseDate(s); d != "" {
		return d
	}
	if len(refISO) < 10 {
		return ""
	}
	year, _ := strconv.Atoi(refISO[:4])
	if year == 0 {
		return ""
	}
	var day, mon int
	for _, m := range dayMonthRe.FindAllStringSubmatch(s, -1) {
		if n, ok := months[strings.ToLower(m[2])]; ok {
			day, _ = strconv.Atoi(m[1])
			mon = n
			break
		}
	}
	if mon == 0 {
		for _, m := range monthDayRe.FindAllStringSubmatch(s, -1) {
			if n, ok := months[strings.ToLower(m[1])]; ok {
				day, _ = strconv.Atoi(m[2])
				mon = n
				break
			}
		}
	}
	if mon == 0 || day < 1 || day > 31 {
		return ""
	}
	d := fmt.Sprintf("%d-%02d-%02d", year, mon, day)
	ref, err := time.Parse("2006-01-02", refISO)
	if err != nil {
		return d
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return ""
	}
	if t.Before(ref.AddDate(0, -1, 0)) {
		return fmt.Sprintf("%d-%02d-%02d", year+1, mon, day)
	}
	return d
}
