package amazon

import "testing"

func TestIsOrdersPage(t *testing.T) {
	cases := map[string]bool{
		"https://www.amazon.de/ab/your-orders":                                                            true,
		"https://www.amazon.es/ab/your-orders?ref_=abn_yo":                                                true,
		"https://www.amazon.es/your-orders/orders":                                                        true,
		"https://www.amazon.de/gp/css/order-history?ref_=nav_orders":                                      true,
		"https://www.amazon.es/ap/signin?openid.return_to=https%3A%2F%2Fwww.amazon.es%2Fab%2Fyour-orders": false,
		"https://www.amazon.de/ap/mfa?arb=x":                                                              false,
		"https://www.amazon.de/":                                                                          false,
		"":                                                                                                false,
	}
	for u, want := range cases {
		if got := IsOrdersPage(u); got != want {
			t.Errorf("IsOrdersPage(%q) = %v, want %v", u, got, want)
		}
	}
}
