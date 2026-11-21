package product

import "testing"

func lowestPage(label, amount string) string {
	return `<html><body><div id="centerCol"><div id="apex_desktop"><span class="a-price"><span class="a-offscreen">325,79€</span></span>
<span class="aok-relative"><span class="a-size-small a-color-secondary srpPriceBlock">` + label + ` <span class="a-price a-text-price srpPriceBlockAUI"><span class="a-offscreen">` + amount + `</span><span aria-hidden="true">` + amount + `</span></span> </span></span></div></div></body></html>`
}

func TestLowest30Languages(t *testing.T) {
	cases := []struct {
		name, label, amount string
		want                float64
	}{
		{"en", "Lowest price in last 30 days:", "€342.94", 342.94},
		{"es", "El precio más bajo de los últimos 30 días:", "342,94€", 342.94},
		{"it", "Prezzo più basso ultimi 30gg:", "1.342,94 €", 1342.94},
		{"fr", "Prix le plus bas des 30 derniers jours :", "322,99 €", 322.99},
		{"de", "Niedrigster Preis der letzten 30 Tage:", "322,99 €", 322.99},
	}
	for _, c := range cases {
		v, cur, ok := Lowest30(lowestPage(c.label, c.amount), "EUR")
		if !ok || v != c.want || cur != "EUR" {
			t.Errorf("%s: got %v %q %v, want %v EUR", c.name, v, cur, ok, c.want)
		}
	}
}

func TestLowest30Absent(t *testing.T) {
	if _, _, ok := Lowest30(`<html><body><div id="apex_desktop"><span class="a-offscreen">325,79€</span></div></body></html>`, "EUR"); ok {
		t.Error("read a 30-day low from a page without one")
	}
}

func TestLowest30IgnoresOtherProducts(t *testing.T) {
	html := `<html><body><div id="apex_desktop"><span class="a-offscreen">325,79€</span></div>
<div class="carousel"><span>Lowest price in last 30 days: <span class="a-offscreen">€229.99</span></span></div></body></html>`
	if _, _, ok := Lowest30(html, "EUR"); ok {
		t.Error("took the 30-day low of a carousel product")
	}
}
