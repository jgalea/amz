// Package product reads facts off a public Amazon detail page. Amazon
// renders the same fact several ways depending on locale, deal state
// and layout experiment, so most fields try a series of strategies, and
// Warnings is what stops a silently empty extraction from reading as a
// clean result.
package product

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type Product struct {
	ASIN        string  `json:"asin"`
	Market      string  `json:"market"`
	URL         string  `json:"url"`
	Title       string  `json:"title,omitempty"`
	Brand       string  `json:"brand,omitempty"`
	Price       float64 `json:"price,omitempty"`
	PriceText   string  `json:"price_text,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	PriceSource string  `json:"price_source,omitempty"`
	ListPrice   float64 `json:"list_price,omitempty"`
	// PriceExVAT is the business-account price when the page shows one.
	PriceExVAT float64 `json:"price_ex_vat,omitempty"`
	// UsedPrice is the cheapest Warehouse / used offer the page advertises.
	UsedPrice float64 `json:"used_price,omitempty"`
	UsedText  string  `json:"used_text,omitempty"`
	// SubscribePrice is the Subscribe & Save price when offered.
	SubscribePrice float64 `json:"subscribe_price,omitempty"`
	Coupon         string  `json:"coupon,omitempty"`
	// OtherOffers is the "New (N) from X" link under the buy box.
	OtherOffers      string  `json:"other_offers,omitempty"`
	OtherOffersCount int     `json:"other_offers_count,omitempty"`
	OtherOffersFrom  float64 `json:"other_offers_from,omitempty"`
	// QuantityTiers are business quantity discounts, as shown.
	QuantityTiers []string `json:"quantity_tiers,omitempty"`
	Availability  string   `json:"availability,omitempty"`
	Unavailable   string   `json:"unavailable_reason,omitempty"`
	SoldBy        string   `json:"sold_by,omitempty"`
	ShipsFrom     string   `json:"ships_from,omitempty"`
	// Delivery is the delivery block: cost, date, import fees.
	Delivery            string      `json:"delivery,omitempty"`
	ImportFees          string      `json:"import_fees,omitempty"`
	DeliveryDestination string      `json:"delivery_destination,omitempty"`
	DestinationType     string      `json:"delivery_destination_type,omitempty"`
	Prime               bool        `json:"prime,omitempty"`
	Rating              float64     `json:"rating,omitempty"`
	RatingCount         int         `json:"rating_count,omitempty"`
	Histogram           map[int]int `json:"histogram,omitempty"`
	VariationCount      int         `json:"variation_count,omitempty"`
	DateFirstAvailable  string      `json:"date_first_available,omitempty"`
	BestSellersRank     string      `json:"best_sellers_rank,omitempty"`
	EAN                 string      `json:"ean,omitempty"`
	Category            string      `json:"category,omitempty"`
	Warnings            []string    `json:"warnings,omitempty"`
}

var (
	ratingRe    = regexp.MustCompile(`(\d+[.,]?\d*)\s*(?:de|out of|von|su|sur|van|z|av)\s*5`)
	intRe       = regexp.MustCompile(`\d[\d.,\s\x{00a0}\x{202f}]*`)
	currencyRe  = regexp.MustCompile(`\b(EUR|GBP|USD|PLN|SEK)\b`)
	eanRe       = regexp.MustCompile(`\b(\d{13}|\d{12}|\d{8})\b`)
	starWords   = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5}
	slotStarRe  = regexp.MustCompile(`histogram_(\d)$`)
	wordStarRe  = regexp.MustCompile(`(?i)histogram-(one|two|three|four|five)Star`)
	distRe      = regexp.MustCompile(`"ratingsDistribution"\s*:\s*(\{.*?\})`)
	glowCTA     = regexp.MustCompile(`(?i)actualizar|elige|selecciona|update|select|choose|aktualisier|mettre à jour|choisir|aggiorna|seleziona|bijwerken|selecteer|zaktualizuj|uppdatera|adresse de livraison|dirección de entrega|delivery address|lieferadresse|indirizzo di consegna|bezorgadres|morada de entrega`)
	glowPrefix  = regexp.MustCompile(`(?i)^(?:entrega en|enviar a|entregar a|deliver(?:ing)? to|ship to|liefer(?:n|ung) nach|livr(?:er|aison) à|spedire a|consegna a|bezorgen in|leveren aan|dostawa do|leverera till)\s*:?\s*`)
	bylineRe    = regexp.MustCompile(`(?i)^(?:Visit the|Brand:|Visita la tienda de|Marca:|Besuche den|Marke:|Visiter la boutique|Marque\s*:|Visita lo Store di|Bezoek de|Merk:)\s*`)
	storeRe     = regexp.MustCompile(`(?i)\s*(?:Store|Shop|-Store|Storefront)$`)
	offerFromRe = regexp.MustCompile(`(?i)(?:from|desde|ab|à partir de|da|vanaf|od|från)\s*([€£$]?\s*[\d.,]+\s*(?:€|£|\$|EUR|GBP|zł|kr)?)`)
	importRe    = regexp.MustCompile(`(?i)import (?:fees|charges|duties)|gastos de importaci|tasas de importaci|frais d'importation|einfuhrgeb|spese di importazione|invoerkosten`)
)

var priceSelectors = []string{
	"#corePriceDisplay_desktop_feature_div .priceToPay .a-offscreen",
	"#corePriceDisplay_desktop_feature_div span.a-price > span.a-offscreen",
	"#corePrice_feature_div span.a-price > span.a-offscreen",
	"#apex_desktop span.a-price > span.a-offscreen",
	"#price_inside_buybox",
	"#newBuyBoxPrice",
	"span.a-price.priceToPay > span.a-offscreen",
}

var firstAvailableKeys = []string{"date first available", "producto en amazon", "fecha de disponibilidad", "im angebot von amazon", "disponibile su amazon", "date de mise en ligne", "datum eerste beschikbaarheid", "first available"}
var rankKeys = []string{"best sellers rank", "clasificación en los más vendidos", "amazon bestseller-rang", "posizione nella classifica", "classement des meilleures ventes", "plaats in bestsellerlijst"}
var eanKeys = []string{"ean", "gtin", "upc", "código de barras", "barcode"}

func text(s *goquery.Selection) string {
	return strings.Join(strings.Fields(s.First().Text()), " ")
}

// Number parses a localised price or count ("1.234,56", "1,234.56",
// "64,99 EUR", "189.593") into a float.
func Number(raw string) (float64, bool) {
	s := strings.NewReplacer(" ", "", " ", "", " ", "").Replace(raw)
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	s = b.String()
	if s == "" {
		return 0, false
	}
	hasComma, hasDot := strings.Contains(s, ","), strings.Contains(s, ".")
	switch {
	case hasComma && hasDot:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasComma:
		if i := strings.LastIndex(s, ","); len(s)-i-1 <= 2 && strings.Count(s, ",") == 1 {
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasDot:
		if i := strings.LastIndex(s, "."); len(s)-i-1 == 3 {
			s = strings.ReplaceAll(s, ".", "")
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func Int(raw string) (int, bool) {
	m := intRe.FindString(raw)
	if m == "" {
		return 0, false
	}
	var b strings.Builder
	for _, r := range m {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	n, err := strconv.Atoi(b.String())
	return n, err == nil
}

func CurrencyOf(s, fallback string) string {
	for sign, code := range map[string]string{"€": "EUR", "£": "GBP", "$": "USD", "zł": "PLN", "kr": "SEK"} {
		if strings.Contains(s, sign) {
			return code
		}
	}
	if m := currencyRe.FindString(s); m != "" {
		return m
	}
	return fallback
}

// Parse reads a detail page. currency is the storefront's, used when the
// price text carries no symbol.
func Parse(html, asin, mkt, url, currency string) Product {
	p := Product{ASIN: strings.ToUpper(asin), Market: mkt, URL: url, Histogram: map[int]int{}}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		p.Warnings = append(p.Warnings, "could not parse the HTML: "+err.Error())
		return p
	}
	// Inline JS inside content containers would otherwise leak into
	// availability and detail bullets.
	doc.Find("script, style, noscript").Remove()

	p.Title = text(doc.Find("#productTitle"))
	if p.Title == "" {
		p.Warnings = append(p.Warnings, "no #productTitle: the page may not be a product detail page")
	}
	if by := text(doc.Find("#bylineInfo")); by != "" {
		p.Brand = strings.TrimSpace(storeRe.ReplaceAllString(bylineRe.ReplaceAllString(by, ""), ""))
	}

	for _, sel := range priceSelectors {
		t := text(doc.Find(sel))
		if v, ok := Number(t); ok && v > 0 {
			p.Price, p.PriceText, p.PriceSource = v, t, sel
			break
		}
	}
	p.Currency = CurrencyOf(p.PriceText, currency)
	if p.Price == 0 {
		stated := text(doc.Find("#outOfStock .a-color-price, #outOfStock .a-color-state"))
		if stated == "" {
			stated = text(doc.Find("#outOfStock"))
		}
		if stated == "" {
			stated = text(doc.Find("#buybox .a-color-price"))
		}
		if stated != "" {
			p.Unavailable = firstSentence(stated, 160)
			p.Warnings = append(p.Warnings, "no price; Amazon states: "+firstSentence(stated, 90))
		} else {
			p.Warnings = append(p.Warnings, "no price found and no unavailability message either; the layout may have changed")
		}
	}
	if v, ok := Number(text(doc.Find(".basisPrice .a-offscreen, #listPrice, .a-text-price .a-offscreen"))); ok {
		p.ListPrice = v
	}
	if v, ok := Number(text(doc.Find("#priceblock_businessprice, #businessPrice .a-offscreen, .businessPrice .a-offscreen, #corePriceDisplay_desktop_feature_div .a-price[data-a-color=\"secondary\"] .a-offscreen"))); ok && v > 0 && v != p.Price {
		p.PriceExVAT = v
	}
	if t := text(doc.Find("#usedBuySection, #usedOfferAccordionRow, #olp_feature_div, #olpLinkWidget_feature_div, #tmmSwatches .a-color-secondary")); t != "" {
		for _, sel := range []string{"#usedBuySection .a-color-price", "#usedBuySection .a-offscreen", "#usedOfferAccordionRow .a-offscreen", "#olp_feature_div .a-color-price", "#olpLinkWidget_feature_div .a-color-price"} {
			if v, ok := Number(text(doc.Find(sel))); ok && v > 0 {
				p.UsedPrice, p.UsedText = v, firstSentence(t, 120)
				break
			}
		}
	}
	if v, ok := Number(text(doc.Find("#snsPrice .a-offscreen, #sns-base-price .a-offscreen, #subscriptionPrice .a-offscreen, #sns-accordion-caption .a-offscreen"))); ok && v > 0 && (p.Price == 0 || v < p.Price*1.5) {
		p.SubscribePrice = v
	}
	if t := text(doc.Find("#olpLinkWidget_feature_div a, #olp_feature_div a, #mbc-top-offer-price-desktop")); t != "" {
		p.OtherOffers = firstSentence(t, 120)
		if n, ok := Int(t); ok {
			p.OtherOffersCount = n
		}
		if m := offerFromRe.FindStringSubmatch(t); m != nil {
			p.OtherOffersFrom, _ = Number(m[1])
		}
	}
	if t := text(doc.Find("#couponBadge, .promoPriceBlockMessage label, #promoPriceBlockMessage_feature_div label, [id^=couponText]")); t != "" {
		p.Coupon = firstSentence(t, 120)
	}
	doc.Find("#quantityDiscount_feature_div tr, #businessQuantityDiscount tr, .qd-tier-row").Each(func(_ int, tr *goquery.Selection) {
		if t := text(tr); t != "" && !strings.Contains(strings.ToLower(t), "quantity") {
			p.QuantityTiers = append(p.QuantityTiers, t)
		}
	})

	avail := text(doc.Find("#availability span"))
	if avail == "" {
		avail = text(doc.Find("#availability"))
	}
	if i := strings.Index(avail, "{"); i >= 0 {
		avail = strings.TrimSpace(avail[:i])
	}
	if len(avail) > 120 {
		avail = avail[:120]
	}
	p.Availability = avail
	p.DeliveryDestination, p.DestinationType = destination(doc)
	p.SoldBy = text(doc.Find("#sellerProfileTriggerId, #merchant-info a, #merchantInfoFeature_feature_div .offer-display-feature-text-message"))
	p.ShipsFrom = text(doc.Find("#fulfillerInfoFeature_feature_div .offer-display-feature-text-message"))
	p.Delivery = firstSentence(text(doc.Find("#mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE, #deliveryBlockMessage, #delivery-message, #mir-layout-DELIVERY_BLOCK")), 200)
	if t := text(doc.Find("#amazonGlobal_feature_div, #ifdBadge, #importFeesDeposit, .ifd-message")); importRe.MatchString(t) {
		p.ImportFees = firstSentence(t, 160)
	} else if importRe.MatchString(p.Delivery) {
		p.ImportFees = p.Delivery
	}
	p.Prime = doc.Find("#primeBadge, .a-icon-prime, [aria-label='Prime'], #isPrimeMember").Length() > 0

	node := doc.Find("#acrPopover, [data-hook='rating-out-of-text'], #averageCustomerReviews .a-icon-alt").First()
	rt, _ := node.Attr("title")
	if rt == "" {
		rt = text(node)
	}
	if m := ratingRe.FindStringSubmatch(rt); m != nil {
		p.Rating, _ = strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	}
	if n, ok := Int(text(doc.Find("#acrCustomerReviewText"))); ok {
		p.RatingCount = n
	} else {
		p.Warnings = append(p.Warnings, "no rating count found")
	}
	p.Histogram = histogram(doc, html)
	if len(p.Histogram) < 4 {
		p.Warnings = append(p.Warnings, "star histogram incomplete ("+strconv.Itoa(len(p.Histogram))+"/5 buckets); distribution checks will be skipped")
	}
	p.VariationCount = doc.Find("#variation_color_name li, #variation_size_name li, #variation_style_name li, #twister li, #twister_feature_div li, [id^='variation_'] li").Length()

	for k, v := range bullets(doc) {
		if p.DateFirstAvailable == "" && hasAny(k, firstAvailableKeys) {
			p.DateFirstAvailable = v
		}
		if p.BestSellersRank == "" && hasAny(k, rankKeys) {
			p.BestSellersRank = truncate(v, 200)
			if i := strings.Index(v, "("); i > 0 {
				p.Category = strings.TrimSpace(v[:i])
			}
		}
		if p.EAN == "" && hasAny(k, eanKeys) {
			if m := eanRe.FindString(v); m != "" {
				p.EAN = m
			}
		}
	}
	if p.Category == "" {
		if crumbs := doc.Find("#wayfinding-breadcrumbs_feature_div a"); crumbs.Length() > 0 {
			var parts []string
			crumbs.Each(func(_ int, a *goquery.Selection) { parts = append(parts, text(a)) })
			p.Category = strings.Join(parts, " > ")
		}
	}
	return p
}

func hasAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// firstSentence keeps the first sentence of a string Amazon runs
// straight into surrounding UI copy.
func firstSentence(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '!') && (s[i+1] == ' ' || (s[i+1] >= 'A' && s[i+1] <= 'Z')) {
			s = s[:i+1]
			break
		}
	}
	return truncate(s, max)
}

// destination reads where Amazon priced the page for out of the "deliver
// to" widget in the nav; a page fetched from another country's IP can
// quote a price the reader will never be offered.
func destination(doc *goquery.Document) (string, string) {
	dest := ""
	for _, sel := range []string{"#glow-ingress-line1", "#glow-ingress-line2"} {
		t := text(doc.Find(sel))
		if t == "" || glowCTA.MatchString(t) {
			continue
		}
		if s := strings.Trim(glowPrefix.ReplaceAllString(t, ""), " :,"); s != "" {
			dest = s
			break
		}
	}
	typ, _ := doc.Find("#glowDestinationType").Attr("value")
	return dest, typ
}

// histogram is the star distribution as percentages. It reads the
// progress bar's aria-valuenow rather than the visible, translated
// label; each row also repeats every star label in hidden spans, so
// the row text itself is not a usable source.
func histogram(doc *goquery.Document, html string) map[int]int {
	hist := map[int]int{}
	doc.Find(`[data-csa-c-content-id^="customerReviews-histogram-"]`).Each(func(_ int, row *goquery.Selection) {
		content, _ := row.Attr("data-csa-c-content-id")
		slot, _ := row.Attr("data-csa-c-slot-id")
		star := 0
		if m := slotStarRe.FindStringSubmatch(slot); m != nil {
			star, _ = strconv.Atoi(m[1])
		} else if m := wordStarRe.FindStringSubmatch(content); m != nil {
			star = starWords[strings.ToLower(m[1])]
		}
		if star == 0 {
			return
		}
		v, ok := row.Find(".a-meter[aria-valuenow], [role='progressbar'][aria-valuenow]").First().Attr("aria-valuenow")
		if !ok {
			return
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			hist[star] = n
		}
	})
	if len(hist) >= 4 {
		return hist
	}
	if m := distRe.FindStringSubmatch(html); m != nil {
		var raw map[string]float64
		if json.Unmarshal([]byte(m[1]), &raw) == nil {
			for k, v := range raw {
				for _, r := range k {
					if r >= '1' && r <= '5' {
						star := int(r - '0')
						if _, ok := hist[star]; !ok {
							if v <= 1 {
								hist[star] = int(v * 100)
							} else {
								hist[star] = int(v)
							}
						}
						break
					}
				}
			}
		}
	}
	return hist
}

func bullets(doc *goquery.Document) map[string]string {
	out := map[string]string{}
	doc.Find("#productDetails_detailBullets_sections1 tr, #productDetails_techSpec_section_1 tr, #detailBullets_feature_div li, #detailBulletsWrapper_feature_div li").Each(func(_ int, row *goquery.Selection) {
		var parts []string
		row.Find("th, td, span").Each(func(_ int, c *goquery.Selection) {
			if c.Children().Length() > 0 && goquery.NodeName(c) == "span" {
				return
			}
			if t := strings.Trim(text(c), " :‎‏"); t != "" {
				parts = append(parts, t)
			}
		})
		if len(parts) >= 2 {
			out[strings.ToLower(parts[0])] = parts[1]
		}
	})
	return out
}
