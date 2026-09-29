package after

import (
	"strings"
	"testing"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/store"
)

var today = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func order(id, date, status string, extra ...string) store.Order {
	o := store.Order{Account: "me", Market: "es", Order: amazon.Order{ID: id, DateISO: date, Total: "45,98 €", Status: status, Items: []amazon.Item{{Title: "Cable USB-C 2 m", ASIN: "B0TEST0001"}}, URL: "https://www.amazon.es/x"}}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "delivered":
			o.DeliveredISO = extra[i+1]
		case "until":
			o.ReturnUntil = extra[i+1]
		}
	}
	return o
}

func TestWindow(t *testing.T) {
	orders := []store.Order{
		order("1", "2026-09-10", "Delivered 12 September", "delivered", "2026-09-12"),
		order("2", "2026-09-01", "Delivered 3 September", "until", "2026-09-30"),
		order("3", "2026-08-01", "Delivered 3 August", "delivered", "2026-08-03"),
		order("4", "2026-09-20", "Return started"),
		order("5", "2026-09-25", ""),
	}
	got := Window(orders, today)
	if len(got) != 3 {
		t.Fatalf("want 3 open windows, got %d: %+v", len(got), got)
	}
	if got[0].OrderID != "2" || got[0].Until != "2026-09-30" || got[0].DaysLeft != 2 || got[0].Estimated {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].OrderID != "1" || got[1].Until != "2026-10-12" || !got[1].Estimated {
		t.Errorf("second: %+v", got[1])
	}
	if got[2].OrderID != "5" || got[2].Until != "2026-10-25" {
		t.Errorf("third (order date fallback): %+v", got[2])
	}
}

func TestWarranty(t *testing.T) {
	orders := []store.Order{
		order("402-0000001-0000001", "2024-03-01", "Delivered"),
		order("402-0000002-0000002", "2021-06-01", "Delivered"),
		order("402-0000003-0000003", "2025-01-01", "Return complete"),
	}
	got := Warranty(orders, "usb cable", "PT", map[string]string{"402-0000001-0000001": "WWW.AMAZON"}, map[string]bool{"402-0000001-0000001": true}, today)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d", len(got))
	}
	if got[0].Years != 3 || got[0].Until != "2027-03-01" || got[0].Expired || got[0].Seller != "WWW.AMAZON" || !got[0].Summary {
		t.Errorf("PT 2024 purchase: %+v", got[0])
	}
	if got[1].Years != 2 || got[1].Until != "2023-06-01" || !got[1].Expired {
		t.Errorf("PT 2021 purchase gets the old two years: %+v", got[1])
	}
	if !got[2].Returned {
		t.Error("returned order should be flagged")
	}
	if n := len(Warranty(orders, "B0TEST0001", "DE", nil, nil, today)); n != 3 {
		t.Errorf("ASIN query matched %d", n)
	}
	if n := len(Warranty(orders, "toaster", "DE", nil, nil, today)); n != 0 {
		t.Errorf("unrelated query matched %d", n)
	}
	if n := len(Warranty(orders, "402-0000002-0000002", "DE", nil, nil, today)); n != 1 {
		t.Errorf("order id query matched %d", n)
	}
}

func TestChaseAndDraft(t *testing.T) {
	orders := []store.Order{order("408-0000001-0000001", "2026-08-10", "Return started"), order("408-0000002-0000002", "2026-08-12", "Return complete")}
	returns := []store.Return{
		{Account: "me", Market: "es", Return: amazon.Return{OrderID: "408-0000001-0000001", Title: "Snorkel kit", Status: "RETURN CREATED", Lines: []string{"RETURN CREATED", "17 Aug, 2026", "RETURNED FROM", "Some Person", "Example Street 1", "Town, 1000-001", "Phone 900000000", "ID of RMA: AbCdEfGhRMA"}}},
		{Account: "me", Market: "es", Return: amazon.Return{OrderID: "408-0000002-0000002", Title: "Refunded thing", Status: "RETURN CREATED", Lines: []string{"RETURN CREATED", "18 Aug, 2026"}}},
	}
	got := Chase(orders, returns, map[string]float64{"408-0000002-0000002": 45.98}, nil, 14, today)
	if len(got) != 1 || got[0].OrderID != "408-0000001-0000001" {
		t.Fatalf("chase = %+v", got)
	}
	l := got[0]
	if l.ReturnDate != "2026-08-17" || l.DaysOpen != 42 || l.RMA != "AbCdEfGhRMA" {
		t.Errorf("line: %+v", l)
	}
	for _, line := range l.Lines {
		if strings.Contains(line, "Phone") || strings.Contains(line, "Street") || strings.Contains(line, "1000-001") || line == "Some Person" {
			t.Errorf("personal line kept: %q", line)
		}
	}
	draft := Draft(l, today)
	for _, want := range []string{"408-0000001-0000001", "AbCdEfGhRMA", "2026-08-17", "Snorkel kit", "45,98 €"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft lacks %q", want)
		}
	}
	if strings.Contains(draft, "900000000") {
		t.Error("draft carries a phone number")
	}
	if n := len(Chase(orders, returns, nil, map[string]amazon.Summary{"408-0000001-0000001": {RefundTotal: 40}}, 14, today)); n != 1 {
		t.Errorf("summary refund should settle the first, unmarked second stays: %d", n)
	}
	if n := len(Chase(orders, returns, nil, nil, 60, today)); n != 0 {
		t.Errorf("minDays not honoured: %d", n)
	}
}
