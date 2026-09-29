package amazon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

var es = Site{TLD: "es"}

func load(t *testing.T, name string) *goquery.Document {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseOrders(t *testing.T) {
	doc := load(t, "order-history.html")
	got := ParseOrders(es, doc)
	if len(got) != 2 {
		t.Fatalf("want 2 orders, got %d: %+v", len(got), got)
	}

	o := got[0]
	if o.ID != "402-1234567-0000001" {
		t.Errorf("id = %q", o.ID)
	}
	if o.Date != "12 de septiembre de 2026" || o.DateISO != "2026-09-12" {
		t.Errorf("date = %q / %q", o.Date, o.DateISO)
	}
	if o.Total != "45,98 €" {
		t.Errorf("total = %q", o.Total)
	}
	if o.Status != "Entregado el 14 de septiembre" {
		t.Errorf("status = %q", o.Status)
	}
	if o.URL != "https://www.amazon.es/gp/your-account/order-details?orderID=402-1234567-0000001" {
		t.Errorf("url = %q", o.URL)
	}
	if !strings.HasPrefix(o.InvoiceURL, "https://www.amazon.es/gp/shared-cs/ajax/invoice/invoice.html?orderId=402-1234567-0000001") {
		t.Errorf("invoice url = %q", o.InvoiceURL)
	}
	if len(o.Items) != 2 {
		t.Fatalf("want 2 items, got %+v", o.Items)
	}
	if o.Items[0].Title != "Cable USB-C a USB-C 2 m, carga rápida 100 W" || o.Items[0].ASIN != "B0TEST0001" || o.Items[0].Qty != "2" {
		t.Errorf("item 0 = %+v", o.Items[0])
	}
	if o.Items[0].URL != "https://www.amazon.es/dp/B0TEST0001" {
		t.Errorf("item 0 url = %q", o.Items[0].URL)
	}
	if o.Items[1].ASIN != "B0TEST0002" || o.Items[1].Qty != "1" {
		t.Errorf("item 1 = %+v", o.Items[1])
	}
	if len(o.Returns) != 0 {
		t.Errorf("delivered order should carry no return markers, got %q", o.Returns)
	}

	r := got[1]
	if r.ID != "402-7654321-0000002" || r.DateISO != "2026-08-03" || r.Total != "129,00 €" {
		t.Errorf("order 2 header = %+v", r)
	}
	if r.Status != "Reembolso emitido" {
		t.Errorf("status = %q", r.Status)
	}
	if len(r.Items) != 1 || r.Items[0].ASIN != "B0TEST0003" {
		t.Errorf("items = %+v", r.Items)
	}
	wantMarkers := []string{"Reembolso emitido", "Tu reembolso de 129,00 € se ha emitido el 20 de agosto de 2026.", "Ver estado de la devolución"}
	if strings.Join(r.Returns, "|") != strings.Join(wantMarkers, "|") {
		t.Errorf("returns = %q, want %q", r.Returns, wantMarkers)
	}
}

func TestParseYearsAndCount(t *testing.T) {
	doc := load(t, "order-history.html")
	years := ParseYears(doc)
	want := []int{2026, 2025, 2024, 2019}
	if len(years) != len(want) {
		t.Fatalf("years = %v", years)
	}
	for i := range want {
		if years[i] != want[i] {
			t.Errorf("years = %v, want %v", years, want)
		}
	}
	if n := ParseOrderCount(doc); n != 23 {
		t.Errorf("count = %d", n)
	}
}

func TestParseOrdersEmptyPage(t *testing.T) {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(`<html><body><div id="ordersContainer"><p>No has realizado ningún pedido en 2019.</p></div></body></html>`))
	if got := ParseOrders(es, doc); len(got) != 0 {
		t.Errorf("want none, got %+v", got)
	}
	if got := ParseYears(doc); len(got) != 0 {
		t.Errorf("want no years, got %v", got)
	}
}

func TestParseTransactions(t *testing.T) {
	doc := load(t, "transactions.html")
	got := ParseTransactions(doc)
	if len(got) != 3 {
		t.Fatalf("want 3 transactions, got %d: %+v", len(got), got)
	}
	refund := got[0]
	if refund.Amount != "+129,00 €" || !refund.Refund || refund.OrderID != "402-7654321-0000002" || refund.DateISO != "2026-08-20" {
		t.Errorf("refund = %+v", refund)
	}
	if refund.Method != "Visa ****1234" || refund.Merchant != "Reembolso: Amazon.es" {
		t.Errorf("refund method/merchant = %q / %q", refund.Method, refund.Merchant)
	}
	charge := got[1]
	if charge.Amount != "-45,98 €" || charge.Refund || charge.OrderID != "402-1234567-0000001" || charge.DateISO != "2026-09-12" {
		t.Errorf("charge = %+v", charge)
	}
	second := got[2]
	if second.Amount != "-9,99 €" || second.Method != "Saldo de la cuenta" || second.OrderID != "402-0000000-0000003" || second.Merchant != "Marketplace Vendor SL" {
		t.Errorf("second charge = %+v", second)
	}
}

func TestParseReturns(t *testing.T) {
	doc := load(t, "returns.html")
	got := ParseReturns(doc)
	if len(got) != 2 {
		t.Fatalf("want 2 returns, got %d: %+v", len(got), got)
	}
	if got[0].OrderID != "402-7654321-0000002" || got[0].Title != "Auriculares inalámbricos con cancelación de ruido" || got[0].Status != "Reembolso completado" {
		t.Errorf("return 0 = %+v", got[0])
	}
	if got[1].OrderID != "402-5555555-0000004" || got[1].Status != "Devolución pendiente" {
		t.Errorf("return 1 = %+v", got[1])
	}
}

func TestParseInvoiceLinks(t *testing.T) {
	doc := load(t, "invoice-popover.html")
	got := ParseInvoiceLinks(es, doc)
	if len(got) != 2 {
		t.Fatalf("want 2 invoice links, got %+v", got)
	}
	if got[0].Label != "Factura 1" || got[0].URL != "https://www.amazon.es/documents/download/0a1b2c3d-1111-2222-3333-444455556666/invoice.pdf" {
		t.Errorf("link 0 = %+v", got[0])
	}
	if got[1].Label != "Factura 2" || !strings.HasPrefix(got[1].URL, "https://www.amazon.es/documents/download/9f8e7d6c") {
		t.Errorf("link 1 = %+v", got[1])
	}
}

func TestParseDate(t *testing.T) {
	cases := map[string]string{
		"12 de septiembre de 2026":      "2026-09-12",
		"3 de agosto de 2026":           "2026-08-03",
		"September 12, 2026":            "2026-09-12",
		"12 September 2026":             "2026-09-12",
		"12. September 2026":            "2026-09-12",
		"12 settembre 2026":             "2026-09-12",
		"12 septembre 2026":             "2026-09-12",
		"12 de setembro de 2026":        "2026-09-12",
		"1 de març de 2026":             "",
		"12/09/2026":                    "2026-09-12",
		"":                              "",
		"Entregado el 14 de septiembre": "",
	}
	for in, want := range cases {
		if got := ParseDate(in); got != want {
			t.Errorf("ParseDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSiteURLs(t *testing.T) {
	uk := Site{TLD: "co.uk"}
	if got := uk.OrdersURL(2025, 20); got != "https://www.amazon.co.uk/gp/css/order-history?startIndex=20&timeFilter=year-2025" {
		t.Errorf("orders url = %q", got)
	}
	if got := uk.OrdersURL(0, 0); got != "https://www.amazon.co.uk/gp/css/order-history" {
		t.Errorf("bare orders url = %q", got)
	}
	if got := uk.Absolute("/dp/B0TEST0001"); got != "https://www.amazon.co.uk/dp/B0TEST0001" {
		t.Errorf("absolute = %q", got)
	}
	if got := uk.Absolute("//m.media-amazon.com/x.jpg"); got != "https://m.media-amazon.com/x.jpg" {
		t.Errorf("absolute protocol-relative = %q", got)
	}
	for _, u := range []string{"https://www.amazon.es/ap/signin?openid=1", "https://www.amazon.es/errors/validateCaptcha", "https://www.amazon.es/ap/cvf/request"} {
		if !IsSignInPage(u) {
			t.Errorf("%q should be a sign-in page", u)
		}
	}
	if IsSignInPage("https://www.amazon.es/gp/css/order-history") {
		t.Error("order history is not a sign-in page")
	}
}

func TestParseSummary(t *testing.T) {
	page := `<html><body><div>Order Summary Item(s) Subtotal: €136.28 Total: €164.89 Gift Card Amount: -€162.26 Grand Total: €0.00</div>
<div>Refund Total €162.26</div><div>Return complete</div><script>var x = "Return started";</script></body></html>`
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(page))
	s := ParseSummary(doc)
	if s.Total != 164.89 || s.RefundTotal != 162.26 || s.OpenReturn {
		t.Errorf("summary = %+v", s)
	}
	open := `<html><body>Total: €49.00 Grand Total: €49.00 Return started Your refund will be processed</body></html>`
	doc, _ = goquery.NewDocumentFromReader(strings.NewReader(open))
	if s := ParseSummary(doc); s.Total != 49 || s.RefundTotal != 0 || !s.OpenReturn {
		t.Errorf("open summary = %+v", s)
	}
}

func TestSiteOwns(t *testing.T) {
	s := Site{TLD: "es"}
	for u, want := range map[string]bool{
		"https://www.amazon.es/documents/download/x/invoice.pdf": true,
		"https://images.amazon.es/x.pdf":                         true,
		"http://www.amazon.es/x":                                 false,
		"https://www.amazon.es.evil.com/x":                       false,
		"https://evil.com/?u=www.amazon.es":                      false,
		"https://user@www.amazon.es/x":                           false,
		"https://www.amazon.de/x":                                false,
		"https://www.amazon.es:8443/x":                           false,
	} {
		if got := s.Owns(u); got != want {
			t.Errorf("Owns(%q) = %v", u, got)
		}
	}
}
