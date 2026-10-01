package product

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Offer is one row of the "all offers" panel: who sells, who ships,
// at what price, in what condition, and how the seller is rated.
type Offer struct {
	Price     float64 `json:"price"`
	PriceText string  `json:"price_text,omitempty"`
	// ExVAT is the price before VAT, shown to business accounts.
	ExVAT       float64 `json:"ex_vat,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Shipping    string  `json:"shipping,omitempty"`
	Condition   string  `json:"condition,omitempty"`
	Seller      string  `json:"seller"`
	SellerID    string  `json:"seller_id,omitempty"`
	SellerURL   string  `json:"seller_url,omitempty"`
	ShipsFrom   string  `json:"ships_from,omitempty"`
	FBA         bool    `json:"fba"`
	Amazon      bool    `json:"amazon"`
	Prime       bool    `json:"prime,omitempty"`
	RatingPct   int     `json:"rating_pct,omitempty"`
	RatingCount int     `json:"rating_count,omitempty"`
	RatingText  string  `json:"rating_text,omitempty"`
	Delivery    string  `json:"delivery,omitempty"`
	Pinned      bool    `json:"pinned,omitempty"`
}

var (
	pctRe      = regexp.MustCompile(`(\d{1,3})\s*%`)
	countRe    = regexp.MustCompile(`\(?([\d.,\s\x{00a0}]+)\s*(?:ratings|valoraciones|opiniones|bewertungen|évaluations|valutazioni|beoordelingen|calificaciones|classificações)`)
	sellerIDRe = regexp.MustCompile(`seller=([A-Z0-9]{10,16})`)
	moneyRe    = regexp.MustCompile(`[€£$]\s?\d[\d.,]*|\d[\d.,]*\s?(?:€|£|\$|EUR|GBP|zł|kr)`)
	amazonRe   = regexp.MustCompile(`(?i)^amazon(\.[a-z.]+)?$|^amazon eu\b|^amazon warehouse|^amazon resale|^amazon segunda mano|^amazon seconde main|^amazon second hand|^amazon retourenkauf`)
)

// OffersURL is the "all offers" panel for an ASIN, the same fragment the
// product page loads when you click "See all buying options". The
// experience is a path segment now (the older experienceId query form
// 404s), and filters is the panel's own JSON, URL-encoded twice as the
// page sends it: {"all":true} lists every condition, adding "new":true
// keeps only new offers.
func OffersURL(base, asin string, used bool) string {
	filters := "%257B%2522all%2522%253Atrue%252C%2522new%2522%253Atrue%257D"
	if used {
		filters = "%257B%2522all%2522%253Atrue%257D"
	}
	return base + "/gp/product/ajax/aodAjaxMain/ref=dp_aod_ALL_mbc?asin=" + strings.ToUpper(asin) + "&pc=dp&filters=" + filters
}

// ParseOffers reads the offers panel. The pinned (buy box) offer comes
// first.
func ParseOffers(html, currency string) []Offer {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	doc.Find("script, style, noscript").Remove()
	var out []Offer
	seen := map[string]bool{}
	doc.Find("#aod-pinned-offer, #aod-offer, .aod-offer, [id^='aod-offer']").Each(func(_ int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		if strings.HasPrefix(id, "aod-offer-") {
			return
		}
		o := parseOffer(s, currency)
		if o.Price == 0 && o.Seller == "" {
			return
		}
		o.Pinned = id == "aod-pinned-offer"
		key := o.Seller + "|" + o.PriceText + "|" + o.Condition
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, o)
	})
	return out
}

func parseOffer(s *goquery.Selection, currency string) Offer {
	var o Offer
	// Popover bodies (the VAT-invoice badge, for one) sit inline next to
	// the seller name.
	s.Find(".a-popover-preload").Remove()
	// A business account gets each offer as an ex-VAT / incl-VAT pair of
	// labelled lines instead of one .a-price.
	if t := text(s.Find("[id^='aod-tax-incl-price']").First()); t != "" {
		o.PriceText = moneyRe.FindString(t)
		o.ExVAT, _ = Number(moneyRe.FindString(text(s.Find("[id^='aod-tax-excl-price']").First())))
	}
	if o.PriceText == "" {
		o.PriceText = text(s.Find(".a-price .a-offscreen, .aok-offscreen, #aod-price-0 .a-offscreen").First())
	}
	if o.PriceText == "" {
		o.PriceText = text(s.Find(".a-price").First())
	}
	o.Price, _ = Number(o.PriceText)
	if o.ExVAT >= o.Price {
		o.ExVAT = 0
	}
	o.Currency = CurrencyOf(o.PriceText, currency)
	o.Shipping = text(s.Find("#aod-bottlingDepositFee, .aod-ship-charge, [id^='aod-shipping']").First())
	// The heading holds the condition, either as plain text or as h5s
	// ("Recommended offer", then the condition, for the pinned offer).
	// A bare h5 elsewhere in the offer can be the product title.
	heading := s.Find("#aod-offer-heading, .aod-offer-heading, #aod-offer-condition").First()
	if hs := heading.Find("h5"); hs.Length() > 0 {
		o.Condition = text(hs.Last())
	} else if heading.Length() > 0 {
		o.Condition = text(heading)
	} else {
		o.Condition = text(s.Find("h5").First())
	}
	sold := s.Find("#aod-offer-soldBy, [id^='aod-offer-soldBy']").First()
	link := sold.Find("a[href*='seller=']").First()
	if link.Length() == 0 {
		link = sold.Find("a").FilterFunction(func(_ int, a *goquery.Selection) bool {
			href, _ := a.Attr("href")
			return !strings.HasPrefix(href, "javascript:")
		}).First()
	}
	o.Seller = text(link)
	if o.Seller == "" {
		o.Seller = firstLine(sold.Find(".a-col-right"))
	}
	if o.Seller == "" {
		o.Seller = lastLine(sold)
	}
	if href, ok := link.Attr("href"); ok {
		o.SellerURL = href
		if m := sellerIDRe.FindStringSubmatch(href); m != nil {
			o.SellerID = m[1]
		}
	}
	o.ShipsFrom = lastLine(s.Find("#aod-offer-shipsFrom, [id^='aod-offer-shipsFrom']").First())
	o.Amazon = amazonRe.MatchString(strings.TrimSpace(o.Seller))
	o.FBA = o.Amazon || amazonRe.MatchString(strings.TrimSpace(o.ShipsFrom))
	o.Prime = s.Find(".a-icon-prime, [aria-label='Prime']").Length() > 0
	o.RatingText = text(s.Find("#aod-offer-seller-rating, [id^='aod-offer-seller-rating']").First())
	if m := pctRe.FindStringSubmatch(o.RatingText); m != nil {
		o.RatingPct, _ = strconv.Atoi(m[1])
	}
	if m := countRe.FindStringSubmatch(strings.ToLower(o.RatingText)); m != nil {
		o.RatingCount, _ = Int(m[1])
	} else if n, ok := Int(strings.TrimLeft(o.RatingText, "0123456789% ")); ok && n > 0 && o.RatingPct > 0 {
		o.RatingCount = n
	}
	o.Delivery = firstSentence(text(s.Find("#aod-offer-delivery, .aod-delivery-promise, [id^='mir-layout-DELIVERY_BLOCK']").First()), 120)
	return o
}

// firstLine is the first leaf of a value column, before any badge that
// follows the value.
func firstLine(s *goquery.Selection) string {
	var out string
	s.Find("span, a").EachWithBreak(func(_ int, c *goquery.Selection) bool {
		if c.Children().Length() == 0 {
			if t := text(c); t != "" && t != "-" {
				out = t
				return false
			}
		}
		return true
	})
	return out
}

// lastLine takes the value from a "label / value" block.
func lastLine(s *goquery.Selection) string {
	var lines []string
	s.Find("span, a").Each(func(_ int, c *goquery.Selection) {
		if c.Children().Length() == 0 {
			if t := text(c); t != "" {
				lines = append(lines, t)
			}
		}
	})
	if len(lines) == 0 {
		return text(s)
	}
	return lines[len(lines)-1]
}
