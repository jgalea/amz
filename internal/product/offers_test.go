package product

import "testing"

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
	if u != "https://www.amazon.es/gp/product/ajax/ref=dp_aod_ALL_mbc?asin=B07PW7J8QG&pc=dp&experienceId=aodAjaxMain&filters=%257B%2522all%2522%253Atrue%252C%2522new%2522%253Atrue%257D" {
		t.Errorf("url = %s", u)
	}
}
