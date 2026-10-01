package product

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// SearchResult is one listing on a storefront search page.
type SearchResult struct {
	Position  int     `json:"position"`
	ASIN      string  `json:"asin"`
	Title     string  `json:"title,omitempty"`
	Brand     string  `json:"brand,omitempty"`
	URL       string  `json:"url,omitempty"`
	Price     float64 `json:"price,omitempty"`
	PriceText string  `json:"price_text,omitempty"`
	Currency  string  `json:"currency,omitempty"`
	// PriceExVAT is the business-account figure when the card shows one.
	PriceExVAT  float64 `json:"price_ex_vat,omitempty"`
	ListPrice   float64 `json:"list_price,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
	RatingCount int     `json:"rating_count,omitempty"`
	Prime       bool    `json:"prime,omitempty"`
	// Sponsored marks a paid placement rendered among the results.
	Sponsored bool   `json:"sponsored,omitempty"`
	Delivery  string `json:"delivery,omitempty"`
}

type SearchPage struct {
	Market              string         `json:"market"`
	URL                 string         `json:"url"`
	Results             []SearchResult `json:"results"`
	DeliveryDestination string         `json:"delivery_destination,omitempty"`
	DestinationType     string         `json:"delivery_destination_type,omitempty"`
	SignedOut           bool           `json:"signed_out,omitempty"`
	Warnings            []string       `json:"warnings,omitempty"`
}

var (
	searchASINRe = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	sponsoredRe  = regexp.MustCompile(`(?i)^\s*(?:patrocinado|sponsored|gesponsert|sponsorisé|sponsorizzato|gesponsord|sponsorowane|sponsrad)\s*$`)
	// sponsoredTitleRe is the prefix the grid writes into a paid card's
	// accessible title ("Anuncio patrocinado: Govee ...").
	sponsoredTitleRe = regexp.MustCompile(`(?i)^(?:anuncio patrocinado|sponsored ad|gesponserte anzeige|annonce sponsorisée|annuncio sponsorizzato|gesponsorde advertentie|reklama sponsorowana|sponsrad annons)\s*[:\-–]?\s*`)
	// thousandsRe reads the abbreviated counts the grid shows ("3,1 mil",
	// "2.1K", "1,2 Tsd.") when the exact figure is not on the page.
	thousandsRe = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(mil|k|tsd|mila|duizend|tys)\b`)
)

const sponsoredSelectors = ".s-sponsored-label-text, .puis-sponsored-label-text, .puis-sponsored-label-info-icon, .s-sponsored-label-info-icon, [data-component-type='sp-sponsored-result'], .s-sponsored-header"

// ParseSearch reads a storefront search page. currency is the
// storefront's, used when the price text carries no symbol.
func ParseSearch(html, mkt, pageURL, currency string) SearchPage {
	sp := SearchPage{Market: mkt, URL: pageURL}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		sp.Warnings = append(sp.Warnings, "could not parse the HTML: "+err.Error())
		return sp
	}
	doc.Find("script, style, noscript").Remove()
	base := ""
	if u, err := url.Parse(pageURL); err == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	sp.DeliveryDestination, sp.DestinationType = destination(doc)
	sp.SignedOut = doc.Find("#nav-item-signout").Length() == 0 && doc.Find("#nav-link-accountList[href*='/ap/signin'], #nav-link-accountList a[href*='/ap/signin']").Length() > 0

	priced := 0
	doc.Find(`div[data-component-type="s-search-result"][data-asin]`).Each(func(i int, s *goquery.Selection) {
		asin, _ := s.Attr("data-asin")
		if !searchASINRe.MatchString(asin) {
			return
		}
		r := SearchResult{Position: len(sp.Results) + 1, ASIN: asin, URL: base + "/dp/" + asin}
		if idx, ok := s.Attr("data-index"); ok {
			if n, err := strconv.Atoi(idx); err == nil {
				r.Position = n
			}
		}
		r.Title, r.Brand = searchTitle(s)
		r.Price, r.PriceText, r.PriceExVAT, r.ListPrice = searchPrices(s)
		r.Currency = CurrencyOf(r.PriceText, currency)
		if m := ratingRe.FindStringSubmatch(text(s.Find(".a-icon-alt"))); m != nil {
			r.Rating, _ = strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		}
		r.RatingCount = ratingCount(s)
		r.Prime = s.Find(".a-icon-prime, i[aria-label*='Prime'], svg[aria-label*='Prime'], .prm-bdg, [data-cy='delivery-block'] [aria-label*='Prime']").Length() > 0
		if stripped := sponsoredTitleRe.ReplaceAllString(r.Title, ""); stripped != r.Title {
			r.Title, r.Sponsored = stripped, true
		}
		r.Sponsored = r.Sponsored || s.Find(sponsoredSelectors).Length() > 0
		if !r.Sponsored {
			s.Find("span.a-color-secondary, span.a-color-base").EachWithBreak(func(_ int, l *goquery.Selection) bool {
				if l.Children().Length() == 0 && sponsoredRe.MatchString(l.Text()) {
					r.Sponsored = true
					return false
				}
				return true
			})
		}
		r.Delivery = firstSentence(text(s.Find("[data-cy='delivery-recipe']")), 120)
		if r.Price > 0 {
			priced++
		}
		sp.Results = append(sp.Results, r)
	})
	switch {
	case len(sp.Results) == 0 && doc.Find(".s-no-results, #noResultsTitle").Length() > 0:
		sp.Warnings = append(sp.Warnings, "Amazon reports no results for this query")
	case len(sp.Results) == 0:
		sp.Warnings = append(sp.Warnings, "no search results found on the page; the layout may have changed")
	case priced == 0:
		sp.Warnings = append(sp.Warnings, "results found but none carries a price; the price layout may have changed")
	}
	if sp.DeliveryDestination == "" {
		sp.Warnings = append(sp.Warnings, "could not read the delivery destination, so the prices are unanchored")
	}
	return sp
}

// ratingCount prefers the exact figure in the link's aria-label
// ("3.127 calificaciones") over the abbreviated visible one ("3,1 mil").
func ratingCount(s *goquery.Selection) int {
	link := s.Find("[data-cy='reviews-block'] a[href*='customerReviews'], a[href*='customerReviews']").First()
	if label, ok := link.Attr("aria-label"); ok {
		if n, ok := Int(label); ok {
			return n
		}
	}
	visible := text(link)
	if m := thousandsRe.FindStringSubmatch(visible); m != nil {
		if v, ok := Number(m[1]); ok {
			return int(v * 1000)
		}
	}
	if n, ok := Int(visible); ok {
		return n
	}
	return 0
}

// searchTitle reads the card's heading. A signed-in layout puts the
// brand in a heading of its own above the titled one.
func searchTitle(s *goquery.Selection) (title, brand string) {
	s.Find("h2").Each(func(_ int, h *goquery.Selection) {
		if t, ok := h.Attr("aria-label"); ok && strings.TrimSpace(t) != "" {
			if title == "" {
				title = strings.Join(strings.Fields(t), " ")
			}
		} else if brand == "" {
			brand = text(h)
		}
	})
	if title == "" {
		title, brand = brand, ""
	}
	return title, brand
}

// searchPrices tells the card's prices apart by what sits next to each:
// a VAT label marks the business pair, a crossed-out figure in the
// secondary colour is the list price, and a-text-price in the base
// colour is the per-unit figure. The first plain price is the offer.
func searchPrices(s *goquery.Selection) (price float64, priceText string, exVAT, list float64) {
	var incl, inclText, plain, plainText = 0.0, "", 0.0, ""
	var listCandidates []float64
	s.Find(".a-price").Each(func(_ int, p *goquery.Selection) {
		t := text(p.Find(".a-offscreen"))
		v, ok := Number(t)
		if !ok || v == 0 {
			return
		}
		label := strings.Join(strings.Fields(p.NextUntil(".a-price").Text()), " ")
		color, _ := p.Attr("data-a-color")
		switch {
		case vatExclRe.MatchString(label) && !vatInclRe.MatchString(label):
			if exVAT == 0 {
				exVAT = v
			}
		case vatInclRe.MatchString(label) && !vatExclRe.MatchString(label):
			if incl == 0 {
				incl, inclText = v, t
			}
		case !p.HasClass("a-text-price"):
			if plain == 0 {
				plain, plainText = v, t
			}
		case color == "secondary":
			listCandidates = append(listCandidates, v)
		}
	})
	price, priceText = plain, plainText
	if incl > 0 {
		price, priceText = incl, inclText
	}
	if exVAT > 0 && (price == 0 || exVAT >= price) {
		if price == 0 {
			price, priceText = exVAT, ""
		}
		exVAT = 0
	}
	for _, v := range listCandidates {
		if v > price {
			list = v
			break
		}
	}
	return price, priceText, exVAT, list
}
