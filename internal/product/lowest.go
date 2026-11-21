package product

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var lowest30Label = regexp.MustCompile(`(?i)^\s*(?:(?:el|la|il|le|der|die|the)\s+)?(lowest price|precio m[aá]s bajo|niedrigster preis|prix le plus bas|prezzo pi[uù] basso)[^:]{0,50}\b30\s*(days|d[ií]as|tage|tagen|jours|giorni|gg)?[^:]{0,20}:`)

var lowest30Scopes = "#apex_desktop, #corePriceDisplay_desktop_feature_div, #corePrice_feature_div, #apex_offerDisplay_desktop, #centerCol"

// Lowest30 reads the "lowest price in the last 30 days" that EU price
// indication rules put beside a discounted price. It only looks inside
// the main product's price area, since carousels of other products carry
// the same label. ok is false when the page shows none.
func Lowest30(html, fallbackCurrency string) (price float64, currency string, ok bool) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0, "", false
	}
	var best *goquery.Selection
	bestLen := 0
	doc.Find(lowest30Scopes).Find("span").Each(func(_ int, s *goquery.Selection) {
		t := strings.Join(strings.Fields(strings.ReplaceAll(s.Text(), " ", " ")), " ")
		if len(t) > 160 || !lowest30Label.MatchString(t) || s.Find(".a-offscreen").Length() == 0 {
			return
		}
		if best == nil || len(t) < bestLen {
			best, bestLen = s, len(t)
		}
	})
	if best == nil {
		return 0, "", false
	}
	raw := strings.TrimSpace(best.Find(".a-offscreen").First().Text())
	v, good := Number(raw)
	if !good || v <= 0 {
		return 0, "", false
	}
	return v, CurrencyOf(raw, fallbackCurrency), true
}
