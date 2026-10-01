package product

import (
	"strings"
	"testing"
)

// amazon.es search page for "Govee H1630", anonymous, priced for
// Portugal; trimmed to the first 24 result cards.
func TestParseSearch(t *testing.T) {
	sp := ParseSearch(load(t, "es_search.html"), "es", "https://www.amazon.es/s?k=Govee+H1630", "EUR")
	if len(sp.Results) != 24 {
		t.Fatalf("results = %d, want the 24 cards in the fixture (%v)", len(sp.Results), sp.Warnings)
	}
	priced, rated := 0, 0
	for _, r := range sp.Results {
		if r.ASIN == "" || r.Title == "" {
			t.Errorf("result %d lacks an ASIN or title: %+v", r.Position, r)
		}
		if !strings.HasPrefix(r.URL, "https://www.amazon.es/dp/"+r.ASIN) {
			t.Errorf("url = %q", r.URL)
		}
		if r.Price > 0 {
			priced++
		}
		if r.Rating > 0 && r.RatingCount > 0 {
			rated++
		}
		if r.Sponsored {
			t.Errorf("%s flagged sponsored; the fixture holds organic results only", r.ASIN)
		}
	}
	if priced < 20 || rated < 20 {
		t.Errorf("priced = %d, rated = %d of 24", priced, rated)
	}
	first := sp.Results[0]
	if first.ASIN != "B0GQXT9Z5T" || first.Position != 2 || !strings.HasPrefix(first.Title, "Govee Lantern") {
		t.Errorf("first = %+v", first)
	}
	if first.Price != 142.30 || first.Currency != "EUR" || first.Rating != 4.7 || first.RatingCount != 66 {
		t.Errorf("first price/rating = %v %s, %v (%d)", first.Price, first.Currency, first.Rating, first.RatingCount)
	}
	if first.Delivery == "" {
		t.Error("delivery line not read")
	}
	byASIN := map[string]SearchResult{}
	for _, r := range sp.Results {
		byASIN[r.ASIN] = r
	}
	// Per-unit and crossed-out figures sit in .a-price too; only the offer
	// is the price, only the crossed-out one is the list price.
	if r := byASIN["B0BWDT7C4Y"]; r.Price != 51.83 || r.ListPrice != 60.98 {
		t.Errorf("B0BWDT7C4Y price = %v, list = %v; want 51.83 and 60.98", r.Price, r.ListPrice)
	}
	if r := byASIN["B0F2FBN7FH"]; r.Price != 172.80 || r.ListPrice != 0 {
		t.Errorf("B0F2FBN7FH price = %v, list = %v; the per-unit figure is not a list price", r.Price, r.ListPrice)
	}
	// The visible count is abbreviated ("3,1 mil"); the link label is exact.
	if r := byASIN["B0BQ23PSFK"]; r.RatingCount != 3127 {
		t.Errorf("B0BQ23PSFK rating count = %d, want 3127", r.RatingCount)
	}
	if sp.DeliveryDestination != "Portugal" || sp.DestinationType != "COUNTRY" {
		t.Errorf("destination = %q / %q", sp.DeliveryDestination, sp.DestinationType)
	}
	if !sp.SignedOut {
		t.Error("an anonymous page must read as signed out")
	}
	if len(sp.Warnings) != 0 {
		t.Errorf("warnings on a clean page: %v", sp.Warnings)
	}
}

func TestParseSearchSponsored(t *testing.T) {
	card := func(asin, extra string) string {
		return `<div data-component-type="s-search-result" data-asin="` + asin + `" data-index="1"><h2 aria-label="Thing ` + asin + `"></h2>` + extra +
			`<span class="a-price"><span class="a-offscreen">10,00 €</span></span></div>`
	}
	html := `<html><body>` +
		card("B000000001", `<span class="puis-sponsored-label-text">Patrocinado</span>`) +
		card("B000000002", `<span class="a-color-secondary">Patrocinado</span>`) +
		card("B000000003", `<span class="a-color-secondary">Entrega GRATIS</span>`) +
		card("B000000004", `<span class="a-color-secondary">Sponsored</span>`) +
		`<div data-component-type="s-search-result" data-asin="B000000005"><h2 aria-label="Anuncio patrocinado: Govee Plafón"></h2></div>` +
		`</body></html>`
	sp := ParseSearch(html, "es", "https://www.amazon.es/s?k=x", "EUR")
	if len(sp.Results) != 5 {
		t.Fatalf("results = %d", len(sp.Results))
	}
	want := []bool{true, true, false, true, true}
	for i, r := range sp.Results {
		if r.Sponsored != want[i] {
			t.Errorf("%s sponsored = %v, want %v", r.ASIN, r.Sponsored, want[i])
		}
	}
	if sp.Results[4].Title != "Govee Plafón" {
		t.Errorf("the ad prefix must leave the title: %q", sp.Results[4].Title)
	}
}

func TestParseSearchEmptyWarns(t *testing.T) {
	sp := ParseSearch("<html><body><p>nothing</p></body></html>", "es", "", "EUR")
	if len(sp.Results) != 0 || len(sp.Warnings) == 0 {
		t.Errorf("an empty page must warn: %+v", sp)
	}
	sp = ParseSearch(`<html><body><div class="s-no-results">Sin resultados</div></body></html>`, "es", "", "EUR")
	if len(sp.Warnings) == 0 || !strings.Contains(sp.Warnings[0], "no results") {
		t.Errorf("Amazon's own no-results page should be named: %v", sp.Warnings)
	}
}

func TestRatingCountAbbreviated(t *testing.T) {
	html := `<div data-component-type="s-search-result" data-asin="B000000001"><h2 aria-label="X"></h2>
	<div data-cy="reviews-block"><a href="/dp/B000000001#customerReviews"><span>(3,1 mil)</span></a></div></div>`
	sp := ParseSearch(html, "es", "", "EUR")
	if len(sp.Results) != 1 || sp.Results[0].RatingCount != 3100 {
		t.Errorf("abbreviated count: %+v", sp.Results)
	}
}

// The same search seen by a signed-in Amazon Business account: the brand
// sits in a heading of its own, the headline price is ex-VAT with the
// incl-VAT figure labelled beside it, and "Recommended" is crossed out.
func TestParseSearchBusiness(t *testing.T) {
	sp := ParseSearch(load(t, "es_search_business.html"), "es", "https://www.amazon.es/s?k=Govee+H1630", "EUR")
	if len(sp.Results) != 3 {
		t.Fatalf("results = %d (%v)", len(sp.Results), sp.Warnings)
	}
	r := sp.Results[0]
	if r.ASIN != "B0GQXT9Z5T" || r.Brand != "Govee" || !strings.HasPrefix(r.Title, "Lantern LED Floor Lamp") {
		t.Errorf("first = %+v", r)
	}
	if r.Price != 132.99 || r.PriceExVAT != 109.91 || r.ListPrice != 139.99 || r.Currency != "EUR" {
		t.Errorf("price = %v, ex-VAT = %v, list = %v %s; want 132.99 incl, 109.91 excl, 139.99 recommended", r.Price, r.PriceExVAT, r.ListPrice, r.Currency)
	}
	if sp.SignedOut {
		t.Error("a page with the sign-out item is not signed out")
	}
	if sp.DeliveryDestination == "" {
		t.Error("destination not read")
	}
}
