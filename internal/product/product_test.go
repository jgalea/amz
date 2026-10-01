package product

import (
	"os"
	"strings"
	"testing"
)

// Golden test against a captured detail page. Amazon changes this markup
// without warning, so the assertions are concrete values; when one fails
// the parser is stale, not the test.
func fixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/es_dp.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParseProduct(t *testing.T) {
	p := Parse(fixture(t), "B09B94956P", "es", "https://www.amazon.es/dp/B09B94956P", "EUR")
	if !strings.Contains(p.Title, "Echo Dot") {
		t.Errorf("title = %q", p.Title)
	}
	if p.Brand != "Amazon" {
		t.Errorf("brand = %q", p.Brand)
	}
	if p.Price != 64.99 || p.Currency != "EUR" {
		t.Errorf("price = %v %s (%s)", p.Price, p.Currency, p.PriceSource)
	}
	if p.Rating != 4.7 || p.RatingCount != 189593 {
		t.Errorf("rating = %v from %d", p.Rating, p.RatingCount)
	}
	if p.Availability != "En stock" {
		t.Errorf("availability = %q", p.Availability)
	}
	sum := 0
	for s := 1; s <= 5; s++ {
		v, ok := p.Histogram[s]
		if !ok {
			t.Errorf("histogram missing %d-star", s)
		}
		sum += v
	}
	if sum != 100 || p.Histogram[5] != 82 {
		t.Errorf("histogram = %v", p.Histogram)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings on a clean page: %v", p.Warnings)
	}
	if p.DeliveryDestination == "" {
		t.Error("delivery destination not read from the glow block")
	}
}

func TestParseReviews(t *testing.T) {
	rs := ParseReviews(fixture(t))
	if len(rs.Reviews) != 13 {
		t.Fatalf("detail page review count changed: %d (%v)", len(rs.Reviews), rs.Warnings)
	}
	for _, r := range rs.Reviews {
		if r.Body == "" {
			t.Errorf("review %s has an empty body", r.ID)
		}
		if strings.Contains(r.Body, "Brief content visible") || strings.Contains(r.Body, "double tap") {
			t.Errorf("review %s body carries teaser text", r.ID)
		}
		if r.Date == "" {
			t.Errorf("review %s date unparsed: %q", r.ID, r.DateRaw)
		}
		if r.Rating < 1 || r.Rating > 5 {
			t.Errorf("review %s rating %v", r.ID, r.Rating)
		}
	}
	if len(rs.Warnings) != 0 {
		t.Errorf("warnings: %v", rs.Warnings)
	}
}

func TestMissingMarkupWarns(t *testing.T) {
	p := Parse("<html><body><p>nothing here</p></body></html>", "B000000000", "es", "", "EUR")
	if len(p.Warnings) == 0 {
		t.Error("an empty page must warn, not read as clean")
	}
	rs := ParseReviews("<html></html>")
	if len(rs.Reviews) != 0 || len(rs.Warnings) == 0 {
		t.Error("no review nodes must warn")
	}
}

func TestNumber(t *testing.T) {
	cases := map[string]float64{
		"1.234,56 €": 1234.56, "€1,234.56": 1234.56, "64,99 EUR": 64.99, "189.593": 189593,
		"1,234": 1234, "12.5": 12.5, "£10": 10, "1 234,50 kr": 1234.5,
	}
	for in, want := range cases {
		got, ok := Number(in)
		if !ok || got != want {
			t.Errorf("Number(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	if _, ok := Number("no digits"); ok {
		t.Error("no digits should fail")
	}
}

func TestDestinationBareCountry(t *testing.T) {
	html := `<html><body><span id="glow-ingress-line1">Enviar a</span><span id="glow-ingress-line2">Portugal</span><input id="glowDestinationType" value="COUNTRY"><span id="productTitle">X</span></body></html>`
	p := Parse(html, "B000000000", "es", "", "EUR")
	if p.DeliveryDestination != "Portugal" || p.DestinationType != "COUNTRY" {
		t.Errorf("destination = %q / %q", p.DeliveryDestination, p.DestinationType)
	}
}

func load(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// amazon.es seen by a signed-in Amazon Business account: the headline
// price is ex-VAT, with the incl-VAT figure labelled next to it and the
// "median price" crossed out above.
func TestParseBusinessPage(t *testing.T) {
	p := Parse(load(t, "es_dp_business.html"), "B0F1CS4VC2", "es", "https://www.amazon.es/dp/B0F1CS4VC2", "EUR")
	if p.Price != 180.49 || p.Currency != "EUR" {
		t.Errorf("price = %v %s (%s): the incl-VAT figure is the price", p.Price, p.Currency, p.PriceSource)
	}
	if p.PriceExVAT != 149.17 {
		t.Errorf("ex-VAT = %v, want 149.17", p.PriceExVAT)
	}
	if p.ListPrice != 189.99 {
		t.Errorf("list price = %v, want the crossed-out 189.99", p.ListPrice)
	}
	if p.SignedOut {
		t.Error("a page with the account menu is not signed out")
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "price") {
			t.Errorf("price warning on a clean business page: %s", w)
		}
	}
}

// amazon.de loaded with cookies saved for amazon.es only: the page is the
// anonymous one, and its crossed-out "was" price is not an ex-VAT price.
func TestParseAnonymousPageWithWasPrice(t *testing.T) {
	p := Parse(load(t, "de_dp_anonymous.html"), "B0F1CS4VC2", "de", "https://www.amazon.de/dp/B0F1CS4VC2", "EUR")
	if p.Price != 146.07 {
		t.Errorf("price = %v, want 146.07", p.Price)
	}
	if p.PriceExVAT != 0 {
		t.Errorf("ex-VAT = %v: the was-price must not be read as ex-VAT", p.PriceExVAT)
	}
	if p.ListPrice != 184.99 {
		t.Errorf("list price = %v, want 184.99", p.ListPrice)
	}
	if !p.SignedOut {
		t.Error("the sign-in link in the account menu means signed out")
	}
}

// The consumer page keeps its values: no ex-VAT, no list price picked up
// from a carousel elsewhere on the page.
func TestParseConsumerPageHasNoBusinessPrice(t *testing.T) {
	p := Parse(fixture(t), "B09B94956P", "es", "https://www.amazon.es/dp/B09B94956P", "EUR")
	if p.PriceExVAT != 0 {
		t.Errorf("ex-VAT = %v on a consumer page", p.PriceExVAT)
	}
	if p.ListPrice != 0 && p.ListPrice < p.Price {
		t.Errorf("list price %v below the price %v", p.ListPrice, p.Price)
	}
}

func TestBusinessPricesByLabel(t *testing.T) {
	html := `<html><body><span id="productTitle">X</span><div id="corePrice_feature_div">
	<div><span class="a-price"><span class="a-offscreen">82,64 €</span></span> sin IVA</div>
	<div><span class="a-price"><span class="a-offscreen">100,00 €</span></span> IVA incluido</div>
	</div><div id="sims-x"><span class="a-text-price"><span class="a-offscreen">12,00 €</span></span></div></body></html>`
	p := Parse(html, "B000000000", "es", "", "EUR")
	if p.Price != 100 || p.PriceExVAT != 82.64 {
		t.Errorf("price = %v, ex-VAT = %v", p.Price, p.PriceExVAT)
	}
	if p.ListPrice != 0 {
		t.Errorf("list price %v read from outside the price block", p.ListPrice)
	}
	// Same labels, the other way round: the pair is read by label, not
	// by order.
	html = `<html><body><span id="productTitle">X</span><div id="corePrice_feature_div">
	<div><span class="a-price"><span class="a-offscreen">100,00 €</span></span> IVA incluido</div>
	<div><span class="a-price"><span class="a-offscreen">82,64 €</span></span> sin IVA</div>
	</div></body></html>`
	p = Parse(html, "B000000000", "es", "", "EUR")
	if p.Price != 100 || p.PriceExVAT != 82.64 {
		t.Errorf("reordered: price = %v, ex-VAT = %v", p.Price, p.PriceExVAT)
	}
}

func TestExVATAbovePriceIsDropped(t *testing.T) {
	html := `<html><body><span id="productTitle">X</span><div id="corePrice_feature_div">
	<span class="a-price priceToPay"><span class="a-offscreen">146,07 €</span></span>
	<span class="priceblock_vat_excl_price"><span class="a-price"><span class="a-offscreen">184,99 €</span></span></span>
	</div></body></html>`
	p := Parse(html, "B000000000", "de", "", "EUR")
	if p.Price != 146.07 || p.PriceExVAT != 0 {
		t.Errorf("price = %v, ex-VAT = %v: an ex-VAT figure above the price is not one", p.Price, p.PriceExVAT)
	}
	if len(p.Warnings) == 0 {
		t.Error("dropping the figure must warn")
	}
}
