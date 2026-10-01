package product

import (
	"os"
	"testing"
)

const aodFixture = `<div id="aod-offer-list">
<div id="aod-pinned-offer">
  <div id="aod-offer-heading"><h5>Nuevo</h5></div>
  <div id="aod-price-0"><span class="a-price"><span class="a-offscreen">15,23 €</span></span></div>
  <div id="aod-offer-shipsFrom"><span class="a-color-tertiary">Vendido por</span><span class="a-color-base">Amazon</span></div>
  <div id="aod-offer-soldBy"><span class="a-color-tertiary">Vendido por</span><a href="/sp?seller=A1EXAMPLE123&ref=x">Pattern EU</a></div>
  <div id="aod-offer-seller-rating"><span>97% positivas en los últimos 12 meses (12.345 valoraciones)</span></div>
</div>
<div id="aod-offer">
  <div id="aod-offer-heading"><h5>Nuevo</h5></div>
  <span class="a-price"><span class="a-offscreen">9,90 €</span></span>
  <div id="aod-offer-shipsFrom"><span>Enviado por</span><span>Tienda Nueva SL</span></div>
  <div id="aod-offer-soldBy"><span>Vendido por</span><a href="/sp?seller=A9NEWSELLER1">Tienda Nueva SL</a></div>
  <div id="aod-offer-seller-rating"><span>80% positivas en los últimos 12 meses (3 valoraciones)</span></div>
</div>
<div id="aod-offer">
  <div id="aod-offer-heading"><h5>De segunda mano - Como nuevo</h5></div>
  <span class="a-price"><span class="a-offscreen">12,00 €</span></span>
  <div id="aod-offer-shipsFrom"><span>Enviado por</span><span>Amazon</span></div>
  <div id="aod-offer-soldBy"><span>Vendido por</span><span>Amazon Segunda mano</span></div>
</div>
</div>`

func TestParseOffers(t *testing.T) {
	offers := ParseOffers(aodFixture, "EUR")
	if len(offers) != 3 {
		t.Fatalf("got %d offers: %+v", len(offers), offers)
	}
	o := offers[0]
	if !o.Pinned || o.Price != 15.23 || o.Seller != "Pattern EU" || o.SellerID != "A1EXAMPLE123" || !o.FBA || o.Amazon || o.RatingPct != 97 || o.RatingCount != 12345 {
		t.Errorf("pinned offer: %+v", o)
	}
	o = offers[1]
	if o.Price != 9.9 || o.FBA || o.Amazon || o.RatingPct != 80 || o.RatingCount != 3 || o.ShipsFrom != "Tienda Nueva SL" {
		t.Errorf("new seller offer: %+v", o)
	}
	o = offers[2]
	if !o.Amazon || !o.FBA || o.Condition != "De segunda mano - Como nuevo" || o.Price != 12 {
		t.Errorf("warehouse offer: %+v", o)
	}
	if got := ParseOffers("<html><body>nothing</body></html>", "EUR"); len(got) != 0 {
		t.Errorf("empty panel gave %+v", got)
	}
}

func TestOffersURL(t *testing.T) {
	u := OffersURL("https://www.amazon.es", "b07pw7j8qg", false)
	if u != "https://www.amazon.es/gp/product/ajax/aodAjaxMain/ref=dp_aod_ALL_mbc?asin=B07PW7J8QG&pc=dp&filters=%257B%2522all%2522%253Atrue%252C%2522new%2522%253Atrue%257D" {
		t.Errorf("new-only url = %s", u)
	}
	u = OffersURL("https://www.amazon.es", "b07pw7j8qg", true)
	if u != "https://www.amazon.es/gp/product/ajax/aodAjaxMain/ref=dp_aod_ALL_mbc?asin=B07PW7J8QG&pc=dp&filters=%257B%2522all%2522%253Atrue%257D" {
		t.Errorf("all-conditions url = %s", u)
	}
}

// A business account's panel prices each offer as an ex-VAT / incl-VAT
// pair of labelled lines rather than one .a-price; the fixture is the
// live amazon.es panel for a product with Warehouse offers, trimmed.
func TestParseOffersBusiness(t *testing.T) {
	raw, err := os.ReadFile("testdata/aod_business.html")
	if err != nil {
		t.Fatal(err)
	}
	offers := ParseOffers(string(raw), "EUR")
	if len(offers) != 4 {
		t.Fatalf("got %d offers: %+v", len(offers), offers)
	}
	o := offers[0]
	if !o.Pinned || o.Price != 69.99 || o.ExVAT != 57.84 || o.PriceText != "€69.99" || o.Currency != "EUR" || !o.Amazon {
		t.Errorf("pinned offer: %+v", o)
	}
	o = offers[1]
	if o.Price != 62.22 || o.ExVAT != 51.42 || o.Condition != "Used - Good" || !o.Amazon || !o.FBA {
		t.Errorf("warehouse offer: %+v", o)
	}
	o = offers[2]
	if o.Price != 62.87 || o.ExVAT != 51.96 || o.Condition != "Used - Very Good" {
		t.Errorf("second warehouse offer: %+v", o)
	}
	o = offers[3]
	if o.Price != 80.36 || o.ExVAT != 0 || o.Condition != "New" || o.Seller != "Amazon UK" {
		t.Errorf("offer without an ex-VAT price: %+v", o)
	}
	for _, o := range offers {
		if o.ExVAT >= o.Price {
			t.Errorf("ex-VAT must be below the price: %+v", o)
		}
	}
}

// amazon.de writes the condition as plain text in the heading and puts
// the product title in an h5 of its own; Amazon's German Warehouse
// seller is "Amazon Retourenkauf".
func TestParseOffersPlainHeading(t *testing.T) {
	html := `<div id="aod-pinned-offer">
  <h5>Some Product Title, Very Long</h5>
  <div id="aod-offer-heading"><span class="a-text-bold"> New </span></div>
  <div id="aod-tax-excl-price-0"><span>€159,66 exkl. MwSt</span></div>
  <div id="aod-tax-incl-price-0"><span>€189,99 inkl. MwSt</span></div>
  <div id="aod-offer-soldBy"><span>Verkauft von</span><a href="/sp?seller=A2EXAMPLE456">Govee EU</a></div>
</div>
<div id="aod-offer">
  <div id="aod-offer-heading"><span class="a-text-bold"> Used - Very Good </span></div>
  <div id="aod-tax-excl-price-1"><span>€149,44 exkl. MwSt</span></div>
  <div id="aod-tax-incl-price-1"><span>€177,83 inkl. MwSt</span></div>
  <div id="aod-offer-shipsFrom"><div class="a-col-right"><span>Amazon</span></div></div>
  <div id="aod-offer-soldBy"><div class="a-col-right"><span>Amazon Retourenkauf</span></div></div>
</div>`
	offers := ParseOffers(html, "EUR")
	if len(offers) != 2 {
		t.Fatalf("got %d offers: %+v", len(offers), offers)
	}
	if o := offers[0]; o.Condition != "New" || o.Price != 189.99 || o.ExVAT != 159.66 || o.Seller != "Govee EU" || o.SellerID != "A2EXAMPLE456" {
		t.Errorf("pinned offer: %+v", o)
	}
	if o := offers[1]; o.Condition != "Used - Very Good" || o.Price != 177.83 || o.Seller != "Amazon Retourenkauf" || !o.Amazon {
		t.Errorf("warehouse offer: %+v", o)
	}
}
