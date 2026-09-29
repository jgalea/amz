package price

import (
	"math"
	"sort"
)

// AccountQuote is what one of your accounts is shown for a product on
// one storefront.
type AccountQuote struct {
	Account  string  `json:"account"`
	Type     string  `json:"type"`
	Market   string  `json:"market"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	PriceEUR float64 `json:"price_eur"`
	// ExVAT is the business price before VAT when the page shows one.
	ExVAT    float64 `json:"ex_vat,omitempty"`
	ExVATEUR float64 `json:"ex_vat_eur,omitempty"`
	Prime    bool    `json:"prime,omitempty"`
	// Effective is what the purchase really costs that account: the
	// ex-VAT figure for a business that reclaims VAT, the shelf price
	// otherwise.
	Effective float64 `json:"effective_eur"`
	Note      string  `json:"note,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// Rank orders account quotes by effective cost. reclaim says whether
// business accounts get their VAT back (a VAT-registered buyer does;
// one buying for personal use does not).
func Rank(qs []AccountQuote, reclaim bool) []AccountQuote {
	out := append([]AccountQuote(nil), qs...)
	for i := range out {
		q := &out[i]
		q.Effective = q.PriceEUR
		if q.Type == "business" && reclaim && q.ExVATEUR > 0 {
			q.Effective = q.ExVATEUR
			q.Note = "ex-VAT, on the basis that the VAT is reclaimed"
		} else if q.Type == "business" && q.ExVATEUR > 0 {
			q.Note = "VAT not reclaimable for this purchase, so the incl-VAT price counts"
		}
		q.Effective = math.Round(q.Effective*100) / 100
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Effective == 0 {
			return false
		}
		if out[j].Effective == 0 {
			return true
		}
		return out[i].Effective < out[j].Effective
	})
	return out
}
