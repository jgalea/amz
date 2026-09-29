package amazon

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseAmount(t *testing.T) {
	cases := map[string]float64{
		"+€99.99":     99.99,
		"-€213.78":    -213.78,
		"−€5.00":      -5,
		"45,98 €":     45.98,
		"+129,00 €":   129,
		"1.234,56 €":  1234.56,
		"$1,234.56":   1234.56,
		"£1,234":      1234,
		"EUR 12.50":   12.5,
		"12":          12,
		"0,99 €":      0.99,
		"-1.000,00 €": -1000,
	}
	for in, want := range cases {
		got, ok := ParseAmount(in)
		if !ok || got != want {
			t.Errorf("ParseAmount(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Refunded", "€"} {
		if _, ok := ParseAmount(in); ok {
			t.Errorf("ParseAmount(%q) should fail", in)
		}
	}
}

func TestParseTransactionsReact(t *testing.T) {
	doc := load(t, "transactions-react.html")
	got := ParseTransactions(doc)
	if len(got) != 3 {
		t.Fatalf("want 3 transactions, got %d: %+v", len(got), got)
	}
	r := got[0]
	if r.DateISO != "2026-08-21" || r.Amount != "+€99.99" || !r.Refund || r.Status != "Refunded" || r.OrderID != "402-1000001-0000001" || r.Merchant != "WWW.AMAZON" || r.Method != "Test Visa ••••0000" {
		t.Errorf("refund row = %+v", r)
	}
	c := got[1]
	if c.DateISO != "2026-08-17" || c.Amount != "-€30.99" || c.Refund || c.Status != "Charged" || len(c.OrderIDs) != 1 {
		t.Errorf("charge row = %+v", c)
	}
	m := got[2]
	if m.Amount != "-€142.37" || len(m.OrderIDs) != 2 || m.OrderIDs[0] != "402-1000003-0000001" || m.OrderIDs[1] != "402-1000001-0000001" || m.OrderID != "402-1000003-0000001" {
		t.Errorf("multi-order charge = %+v", m)
	}
}

func TestParseGiftCardTable(t *testing.T) {
	html := `<html><body><table id="gc-activity">
<tr><th>Date</th><th>Description</th><th>Amount</th></tr>
<tr><td>20 August 2026</td><td>Refund for Order #402-1000001-0000001</td><td>+€25.00</td></tr>
<tr><td>12 August 2026</td><td>Used on Order #402-1000002-0000001</td><td>-€12.99</td></tr>
<tr><td>1 August 2026</td><td>Gift card redeemed</td><td>€50.00</td></tr>
</table></body></html>`
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got := ParseGiftCard(doc)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %+v", got)
	}
	if got[0].DateISO != "2026-08-20" || got[0].Amount != "+€25.00" || got[0].OrderID != "402-1000001-0000001" || got[0].Description != "Refund for Order #402-1000001-0000001" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Amount != "-€12.99" || got[1].OrderID != "402-1000002-0000001" {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[2].OrderID != "" || got[2].Amount != "€50.00" {
		t.Errorf("entry 2 = %+v", got[2])
	}
}

func TestParseGiftCardLeaves(t *testing.T) {
	html := `<html><body><div><span>20 August 2026</span><span>Refund</span><span>Order #402-1000001-0000001</span><span>+€25.00</span></div>
<div><span>12 August 2026</span><span>Used on order</span><span>Order #402-1000002-0000001</span><span>-€12.99</span></div></body></html>`
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got := ParseGiftCard(doc)
	if len(got) != 2 || got[0].OrderID != "402-1000001-0000001" || got[0].Amount != "+€25.00" || got[1].Amount != "-€12.99" {
		t.Errorf("leaves = %+v", got)
	}
}

func TestParseOrdersIgnoresInlineScripts(t *testing.T) {
	html := `<html><body><div class="order-card"><div class="order-header"><span dir="ltr">402-1000001-0000001</span></div>
<script>(function(s) { var c = s.previousElementSibling; if (items.length <= 4) { return; } })</script>
<div class="delivery-box"><span class="delivery-box__primary-text">Delivered 2 August</span></div></div></body></html>`
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got := ParseOrders(es, doc)
	if len(got) != 1 || len(got[0].Returns) != 0 {
		t.Errorf("script text leaked into returns: %+v", got)
	}
}

func TestParseDateComma(t *testing.T) {
	if got := ParseDate("17 Aug, 2026"); got != "2026-08-17" {
		t.Errorf("ParseDate(17 Aug, 2026) = %q", got)
	}
}

func TestParseDateNear(t *testing.T) {
	cases := []struct{ in, ref, want string }{
		{"Delivered 14 September", "2026-09-12", "2026-09-14"},
		{"Entregado el 3 de octubre", "2026-09-28", "2026-10-03"},
		{"Delivered 2 January", "2026-12-28", "2027-01-02"},
		{"Delivered September 14", "2026-09-12", "2026-09-14"},
		{"Return eligible until 14 October 2026", "2026-09-12", "2026-10-14"},
		{"Delivered", "2026-09-12", ""},
		{"Delivered 14 September", "", ""},
	}
	for _, c := range cases {
		if got := ParseDateNear(c.in, c.ref); got != c.want {
			t.Errorf("ParseDateNear(%q, %q) = %q, want %q", c.in, c.ref, got, c.want)
		}
	}
}
