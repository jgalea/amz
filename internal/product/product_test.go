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
