// Package price compares one product across storefronts and across
// your own accounts.
//
// Cross-border caveats before acting on a ranking: listed prices include
// each country's VAT, and rates differ; shipping to your address is not
// in the listed price; Amazon prices a page for the destination it
// infers from the requesting IP, which is why each quote records the
// destination; a UK order into the EU can attract import VAT and duty;
// and the same ASIN occasionally maps to a different pack size in
// another storefront, which is why the title is carried through.
package price

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"

	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/fx"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/product"
)

type Quote struct {
	Market       string   `json:"market"`
	Country      string   `json:"country"`
	ASIN         string   `json:"asin"`
	URL          string   `json:"url"`
	Title        string   `json:"title,omitempty"`
	Price        float64  `json:"price,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	PriceEUR     float64  `json:"price_eur,omitempty"`
	ListPrice    float64  `json:"list_price,omitempty"`
	UsedPrice    float64  `json:"used_price,omitempty"`
	UsedEUR      float64  `json:"used_eur,omitempty"`
	Subscribe    float64  `json:"subscribe_price,omitempty"`
	Coupon       string   `json:"coupon,omitempty"`
	Tiers        []string `json:"quantity_tiers,omitempty"`
	Availability string   `json:"availability,omitempty"`
	Delivery     string   `json:"delivery,omitempty"`
	ImportFees   string   `json:"import_fees,omitempty"`
	Destination  string   `json:"delivery_destination,omitempty"`
	SoldBy       string   `json:"sold_by,omitempty"`
	ShipsFrom    string   `json:"ships_from,omitempty"`
	Prime        bool     `json:"prime,omitempty"`
	// Matched says how the ASIN was found on this storefront when it is
	// not the one asked for: "ean" or "title".
	Matched  string           `json:"matched,omitempty"`
	Error    string           `json:"error,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
	Product  *product.Product `json:"-"`
}

type Comparison struct {
	ASIN     string  `json:"asin"`
	EAN      string  `json:"ean,omitempty"`
	Base     string  `json:"base_currency"`
	Checked  int     `json:"checked"`
	Priced   int     `json:"priced"`
	Cheapest string  `json:"cheapest,omitempty"`
	Spread   float64 `json:"spread_pct,omitempty"`
	FXError  string  `json:"fx_error,omitempty"`
	Quotes   []Quote `json:"quotes"`
}

type Options struct {
	Refresh bool
	// Match looks the product up by EAN (then title and brand) on
	// storefronts where the ASIN is not listed. It costs a search page per
	// storefront.
	Match bool
	Base  string
	// Country is where the buyer takes delivery, two letters. A page
	// priced for another destination gets a warning; one priced for
	// this country does not, even when it is not the storefront's own.
	Country string
}

// quote fetches and reads one storefront's detail page.
func quote(asin string, sf market.Storefront, refresh bool, country string) Quote {
	q := Quote{Market: sf.Code, Country: sf.Country, ASIN: strings.ToUpper(asin), URL: fetch.DetailURL(asin, sf)}
	if !sf.ASINShared {
		q.Warnings = append(q.Warnings, sf.Code+" usually assigns its own ASINs; a hit here may be a different product")
	}
	page, err := fetch.Detail(asin, sf, refresh)
	if err != nil {
		q.Error = err.Error()
		return q
	}
	p := product.Parse(page.HTML, asin, sf.Code, page.URL, sf.Currency)
	fill(&q, p, sf, country)
	return q
}

func fill(q *Quote, p product.Product, sf market.Storefront, country string) {
	q.Product = &p
	q.Title = p.Title
	q.Price, q.Currency = p.Price, p.Currency
	if q.Currency == "" {
		q.Currency = sf.Currency
	}
	q.ListPrice, q.UsedPrice, q.Subscribe, q.Coupon, q.Tiers = p.ListPrice, p.UsedPrice, p.SubscribePrice, p.Coupon, p.QuantityTiers
	q.Availability, q.Delivery, q.ImportFees, q.Destination = p.Availability, p.Delivery, p.ImportFees, p.DeliveryDestination
	q.SoldBy, q.ShipsFrom, q.Prime = p.SoldBy, p.ShipsFrom, p.Prime
	q.Warnings = append(q.Warnings, p.Warnings...)
	if p.DeliveryDestination != "" && p.DestinationType == "COUNTRY" {
		want := strings.ToLower(sf.Country)
		if country != "" {
			want = strings.ToLower(CountryName(country))
		}
		d := strings.ToLower(p.DeliveryDestination)
		if d != want && d != strings.ToLower(sf.CountryLocal) && !strings.Contains(d, want) {
			q.Warnings = append(q.Warnings, fmt.Sprintf("priced for delivery to %s, not %s", p.DeliveryDestination, strings.Title(want)))
		}
	} else if p.DeliveryDestination == "" {
		q.Warnings = append(q.Warnings, "could not read the delivery destination, so the price is unanchored")
	}
	if p.Price == 0 {
		q.Error = p.Unavailable
		if q.Error == "" {
			q.Error = "listed, but no price could be read from the page"
		}
	}
}

var asinAttrRe = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// search runs a storefront search and returns the first result ASIN
// (sponsored results skipped), or "".
func search(sf market.Storefront, query string, refresh bool) (string, error) {
	u := sf.Base() + "/s?k=" + strings.ReplaceAll(strings.TrimSpace(query), " ", "+")
	page, err := fetch.Get(u, sf, refresh)
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page.HTML))
	if err != nil {
		return "", err
	}
	found := ""
	doc.Find(`div[data-component-type="s-search-result"][data-asin]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		a, _ := s.Attr("data-asin")
		if !asinAttrRe.MatchString(a) || s.Find(".s-sponsored-label-text, [data-component-type='sp-sponsored-result']").Length() > 0 {
			return true
		}
		found = a
		return false
	})
	return found, nil
}

// Compare fetches the ASIN on every storefront given and converts the
// prices to the base currency. With Match set, storefronts that do not
// list the ASIN are searched by EAN, then title and brand.
func Compare(asin string, sfs []market.Storefront, o Options) Comparison {
	if o.Base == "" {
		o.Base = "EUR"
	}
	asin = strings.ToUpper(asin)
	cmp := Comparison{ASIN: asin, Base: o.Base, Quotes: make([]Quote, len(sfs))}
	var wg sync.WaitGroup
	for i, sf := range sfs {
		wg.Add(1)
		go func(i int, sf market.Storefront) {
			defer wg.Done()
			cmp.Quotes[i] = quote(asin, sf, o.Refresh, o.Country)
		}(i, sf)
	}
	wg.Wait()

	if o.Match {
		var ref *product.Product
		for i := range cmp.Quotes {
			if p := cmp.Quotes[i].Product; p != nil && p.Title != "" {
				ref = p
				if p.EAN != "" {
					cmp.EAN = p.EAN
					break
				}
			}
		}
		if ref != nil {
			for i := range cmp.Quotes {
				q := &cmp.Quotes[i]
				if q.Product != nil && q.Product.Title != "" {
					continue
				}
				sf, _ := market.Get(q.Market)
				how, query := "ean", cmp.EAN
				if query == "" {
					how, query = "title", firstWords(ref.Title, 8)
					if ref.Brand != "" && !strings.HasPrefix(strings.ToLower(ref.Title), strings.ToLower(ref.Brand)) {
						query = ref.Brand + " " + query
					}
				}
				found, err := search(sf, query, o.Refresh)
				if err != nil || found == "" || found == asin {
					if err != nil {
						q.Warnings = append(q.Warnings, "search: "+err.Error())
					}
					continue
				}
				nq := quote(found, sf, o.Refresh, o.Country)
				nq.Matched = how
				if how == "title" {
					nq.Warnings = append(nq.Warnings, "matched by title and brand, not by EAN; check it is the same product and pack size")
				}
				cmp.Quotes[i] = nq
			}
		}
	}

	table, err := fx.Rates()
	if err != nil {
		cmp.FXError = err.Error()
	}
	low, high := math.MaxFloat64, 0.0
	for i := range cmp.Quotes {
		q := &cmp.Quotes[i]
		cmp.Checked++
		if q.Price == 0 {
			continue
		}
		if table != nil {
			if v, ok := table.Convert(q.Price, q.Currency, o.Base); ok {
				q.PriceEUR = math.Round(v*100) / 100
			} else {
				q.Warnings = append(q.Warnings, "no ECB rate for "+q.Currency+", left unconverted")
			}
			if q.UsedPrice > 0 {
				if v, ok := table.Convert(q.UsedPrice, q.Currency, o.Base); ok {
					q.UsedEUR = math.Round(v*100) / 100
				}
			}
		}
		if q.PriceEUR == 0 {
			continue
		}
		cmp.Priced++
		if q.PriceEUR < low {
			low, cmp.Cheapest = q.PriceEUR, q.Market
		}
		if q.PriceEUR > high {
			high = q.PriceEUR
		}
	}
	if cmp.Priced >= 2 && low > 0 {
		cmp.Spread = math.Round((high-low)/low*1000) / 10
	}
	return cmp
}

func firstWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) > n {
		w = w[:n]
	}
	return strings.Join(w, " ")
}

// FromPage builds a quote from a page fetched some other way (a
// signed-in session), so the same parser and warnings apply.
func FromPage(asin string, sf market.Storefront, html, url, country string) Quote {
	q := Quote{Market: sf.Code, Country: sf.Country, ASIN: strings.ToUpper(asin), URL: url}
	p := product.Parse(html, asin, sf.Code, url, sf.Currency)
	fill(&q, p, sf, country)
	return q
}

// CountryName is the English name Amazon's delivery widget shows for a
// country code; unknown codes come back unchanged.
func CountryName(cc string) string {
	if s, ok := market.ByCountry(cc); ok && strings.EqualFold(s.CountryCode, cc) {
		return s.Country
	}
	names := map[string]string{"PT": "Portugal", "AT": "Austria", "CH": "Switzerland", "LU": "Luxembourg", "DK": "Denmark", "FI": "Finland", "NO": "Norway", "CZ": "Czechia", "GR": "Greece", "HU": "Hungary", "RO": "Romania", "MT": "Malta", "CY": "Cyprus", "HR": "Croatia", "SI": "Slovenia", "SK": "Slovakia", "BG": "Bulgaria", "EE": "Estonia", "LV": "Latvia", "LT": "Lithuania"}
	if n, ok := names[strings.ToUpper(cc)]; ok {
		return n
	}
	return cc
}
