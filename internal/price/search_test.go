package price

import (
	"testing"

	"github.com/jgalea/amz/internal/market"
)

func searchFixture() string {
	card := func(asin, title, extra string) string {
		return `<div data-component-type="s-search-result" data-asin="` + asin + `"><h2 aria-label="` + title + `"></h2>` + extra +
			`<span class="a-price"><span class="a-offscreen">10,00 €</span></span></div>`
	}
	return `<html><body><span id="glow-ingress-line1">Enviar a</span><span id="glow-ingress-line2">Portugal</span><input id="glowDestinationType" value="COUNTRY">` +
		card("B000000001", "Govee lamp", `<span class="puis-sponsored-label-text">Patrocinado</span>`) +
		card("B000000002", "Govee strip", "") +
		card("B000000003", "Philips bulb", "") +
		card("B000000004", "Govee bulb", "") +
		`</body></html>`
}

func TestSearchFromPageFilters(t *testing.T) {
	es, _ := market.Get("es")
	s := SearchFromPage(es, searchFixture(), "https://www.amazon.es/s?k=govee", SearchOptions{Country: "PT"})
	if s.Found != 4 || s.Sponsored != 1 || len(s.Listings) != 4 {
		t.Errorf("unfiltered: found %d, sponsored %d, listed %d", s.Found, s.Sponsored, len(s.Listings))
	}
	if !s.Listings[0].Sponsored {
		t.Error("the sponsored card must stay flagged when kept")
	}
	if len(s.Warnings) != 0 {
		t.Errorf("a page priced for the buyer's country warns: %v", s.Warnings)
	}

	s = SearchFromPage(es, searchFixture(), "", SearchOptions{NoSponsored: true, Brand: "govee"})
	if len(s.Listings) != 2 || s.Listings[0].ASIN != "B000000002" || s.Listings[1].ASIN != "B000000004" {
		t.Errorf("no-sponsored + brand: %+v", s.Listings)
	}
	if s.Found != 4 || s.Sponsored != 1 {
		t.Errorf("counts must describe the page, not the filtered list: %d / %d", s.Found, s.Sponsored)
	}

	s = SearchFromPage(es, searchFixture(), "", SearchOptions{Limit: 1, NoSponsored: true})
	if len(s.Listings) != 1 || s.Listings[0].ASIN != "B000000002" {
		t.Errorf("limit: %+v", s.Listings)
	}
	s = SearchFromPage(es, searchFixture(), "", SearchOptions{Limit: 1, Brand: "philips"})
	if len(s.Listings) != 1 || s.Found != 4 || s.Sponsored != 1 {
		t.Errorf("a limit must not stop the page counts short: listed %d, found %d, sponsored %d", len(s.Listings), s.Found, s.Sponsored)
	}

	s = SearchFromPage(es, searchFixture(), "", SearchOptions{Brand: "ikea"})
	if len(s.Listings) != 0 || len(s.Warnings) == 0 {
		t.Errorf("filtering everything out must warn: %+v", s)
	}
}

func TestSearchFromPageDestinationWarning(t *testing.T) {
	de, _ := market.Get("de")
	s := SearchFromPage(de, searchFixture(), "", SearchOptions{})
	if len(s.Warnings) == 0 {
		t.Error("a de page priced for Portugal, with no buyer country, must warn")
	}
}
