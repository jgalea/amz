package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jgalea/amz/internal/amazon"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOrdersRoundTrip(t *testing.T) {
	s := open(t)
	orders := []amazon.Order{
		{ID: "402-0000001-0000001", Date: "12 de septiembre de 2026", DateISO: "2026-09-12", Total: "45,98 €", Status: "Entregado el 14 de septiembre",
			Items: []amazon.Item{{Title: "Cable USB-C", ASIN: "B0TEST0001", Qty: "2"}, {Title: "Funda", ASIN: "B0TEST0002", Qty: "1"}}, ReturnUntil: "2026-10-14"},
		{ID: "402-0000002-0000002", DateISO: "2025-01-03", Total: "£10.00", Items: []amazon.Item{{Title: "Old thing", ASIN: "B0TEST0003"}}},
	}
	if err := s.UpsertOrders("me", "es", "EUR", orders); err != nil {
		t.Fatal(err)
	}
	got, err := s.Orders(Query{Accounts: []string{"me"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "402-0000001-0000001" {
		t.Fatalf("Orders() = %+v", got)
	}
	if got[0].TotalN != 45.98 || got[0].Currency != "EUR" || len(got[0].Items) != 2 || got[0].Items[0].Qty != "2" || got[0].ReturnUntil != "2026-10-14" {
		t.Errorf("first order: %+v", got[0])
	}
	if got[1].Currency != "GBP" {
		t.Errorf("currency from symbol: %q", got[1].Currency)
	}

	// Re-upserting replaces status and items rather than duplicating.
	orders[0].Status = "Devolución completada"
	orders[0].Items = orders[0].Items[:1]
	if err := s.UpsertOrders("me", "es", "EUR", orders[:1]); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Orders(Query{OrderID: "402-0000001-0000001"})
	if len(got) != 1 || got[0].Status != "Devolución completada" || len(got[0].Items) != 1 {
		t.Errorf("upsert did not replace: %+v", got)
	}

	byASIN, _ := s.Orders(Query{ASIN: "b0test0003"})
	if len(byASIN) != 1 || byASIN[0].ID != "402-0000002-0000002" {
		t.Errorf("ASIN filter: %+v", byASIN)
	}
	byGrep, _ := s.Orders(Query{Grep: "usb"})
	if len(byGrep) != 1 {
		t.Errorf("grep filter: %+v", byGrep)
	}
	since, _ := s.Orders(Query{Since: "2026-01-01"})
	if len(since) != 1 {
		t.Errorf("since filter: %+v", since)
	}
	none, _ := s.Orders(Query{Markets: []string{"de"}})
	if len(none) != 0 {
		t.Errorf("market filter: %+v", none)
	}
}

func TestTransactionsKeepDuplicatesButNotRepeats(t *testing.T) {
	s := open(t)
	tx := []amazon.Transaction{
		{DateISO: "2026-08-21", Amount: "+€99.99", Method: "Visa", OrderIDs: []string{"408-0000001-0000001"}, Status: "Refunded", Refund: true},
		{DateISO: "2026-08-21", Amount: "+€99.99", Method: "Visa", OrderIDs: []string{"408-0000001-0000001"}, Status: "Refunded", Refund: true},
		{DateISO: "2026-08-20", Amount: "-€12.00", Method: "Visa", OrderIDs: []string{"408-0000002-0000002"}, Status: "Charged"},
	}
	n, err := s.UpsertTransactions("me", "es", tx)
	if err != nil || n != 3 {
		t.Fatalf("first upsert added %d, %v", n, err)
	}
	n, err = s.UpsertTransactions("me", "es", tx)
	if err != nil || n != 0 {
		t.Fatalf("second upsert added %d, %v; want 0", n, err)
	}
	got, _ := s.Transactions(Query{})
	if len(got) != 3 {
		t.Fatalf("stored %d transactions", len(got))
	}
	if got[0].AmountN != 99.99 || !got[0].Refund || got[0].OrderID != "408-0000001-0000001" {
		t.Errorf("row: %+v", got[0])
	}
	if got[2].AmountN != -12 {
		t.Errorf("charge sign: %+v", got[2])
	}
}

func TestGiftCardReturnsSummariesSyncs(t *testing.T) {
	s := open(t)
	n, err := s.UpsertGiftCard("me", "es", []amazon.GiftCardEntry{{DateISO: "2026-07-01", Amount: "+€5.00", Description: "Refund", OrderID: "402-0000001-0000001"}})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	gc, _ := s.GiftCard(Query{})
	if len(gc) != 1 || gc[0].AmountN != 5 {
		t.Errorf("giftcard: %+v", gc)
	}
	if err := s.UpsertReturns("me", "es", []amazon.Return{{OrderID: "402-0000001-0000001", Title: "Cable", Status: "RETURN CREATED", Lines: []string{"a", "b"}}}); err != nil {
		t.Fatal(err)
	}
	rets, _ := s.Returns(Query{Accounts: []string{"me"}})
	if len(rets) != 1 || len(rets[0].Lines) != 2 {
		t.Errorf("returns: %+v", rets)
	}
	if err := s.UpsertSummaries("me", "es", map[string]amazon.Summary{"402-0000001-0000001": {Total: 45.98, RefundTotal: 40, OpenReturn: true}}); err != nil {
		t.Fatal(err)
	}
	sums, _ := s.Summaries("me", "es")
	if sums["402-0000001-0000001"].RefundTotal != 40 || !sums["402-0000001-0000001"].OpenReturn {
		t.Errorf("summaries: %+v", sums)
	}
	if !s.LastSync("me", "es", "orders").IsZero() {
		t.Error("no sync yet")
	}
	if err := s.SetSync("me", "es", "orders"); err != nil {
		t.Fatal(err)
	}
	if time.Since(s.LastSync("me", "es", "orders")) > time.Minute {
		t.Error("sync stamp not fresh")
	}
	census, _ := s.Census()
	if len(census) != 0 {
		t.Errorf("census without orders: %+v", census)
	}
}

func TestPricesWatchesNotices(t *testing.T) {
	s := open(t)
	if err := s.AddPrice(Price{ASIN: "b0test0001", Market: "es", Price: 10, Currency: "EUR", PriceEUR: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPrice(Price{ASIN: "B0TEST0001", Market: "de", Price: 9, Currency: "EUR", PriceEUR: 9, Account: "me"}); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Prices("B0TEST0001", time.Now().Add(-time.Hour))
	if len(ps) != 2 {
		t.Errorf("prices: %+v", ps)
	}
	id, err := s.AddWatch(Watch{ASIN: "B0TEST0001", Markets: "es,de", Below: 8})
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	if err := s.TouchWatch(id, 9, "de", "Cable", false); err != nil {
		t.Fatal(err)
	}
	ws, _ := s.Watches()
	if len(ws) != 1 || ws[0].LastPrice != 9 || ws[0].Title != "Cable" || ws[0].NotifiedAt != "" {
		t.Errorf("watch: %+v", ws)
	}
	s.TouchWatch(id, 7, "de", "", true)
	ws, _ = s.Watches()
	if ws[0].NotifiedAt == "" || ws[0].Title != "Cable" {
		t.Errorf("notified watch: %+v", ws)
	}
	if ok, _ := s.RemoveWatch(id); !ok {
		t.Error("remove")
	}
	fresh, err := s.AddNotice(Notice{MessageID: "<m1>", Subject: "Account protection", Kind: "restriction"})
	if err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	fresh, _ = s.AddNotice(Notice{MessageID: "<m1>", Subject: "Account protection", Kind: "restriction"})
	if fresh {
		t.Error("same message twice should not be new")
	}
}
