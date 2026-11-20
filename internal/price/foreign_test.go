package price

import (
	"testing"

	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/product"
)

func TestFillFlagsForeignPriceWithoutCurrency(t *testing.T) {
	de, err := market.Get("de")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, text, dest string
		want             bool
	}{
		{"chile, no currency", "221.820", "Chile", true},
		{"chile, explicit code", "CLP 221.820", "Chile", false},
		{"portugal, euro sign", "221,82 €", "Portugal", false},
		{"germany, no currency", "221,82", "Deutschland", false},
	}
	for _, tc := range cases {
		var q Quote
		fill(&q, product.Product{Price: 221820, PriceText: tc.text, Currency: "EUR", DeliveryDestination: tc.dest, DestinationType: "COUNTRY"}, de, "")
		if q.ForeignPrice != tc.want {
			t.Errorf("%s: ForeignPrice = %v, want %v (warnings %v)", tc.name, q.ForeignPrice, tc.want, q.Warnings)
		}
	}
}
