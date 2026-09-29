package money

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/store"
)

func order(acc, mkt, id, date, total string, n int) store.Order {
	t, _ := amazon.ParseAmount(total)
	o := store.Order{Account: acc, Market: mkt, Order: amazon.Order{ID: id, DateISO: date, Total: total}, TotalN: t, Currency: "EUR"}
	for i := 0; i < n; i++ {
		o.Items = append(o.Items, amazon.Item{Title: "Item"})
	}
	return o
}

func TestSpend(t *testing.T) {
	orders := []store.Order{
		order("home", "es", "1", "2026-01-10", "€10.00", 1),
		order("home", "es", "2", "2026-01-20", "€20.50", 2),
		order("work", "de", "3", "2026-02-01", "€100.00", 1),
		order("home", "es", "4", "2025-12-31", "€5.00", 1),
	}
	got := Spend(orders, "month", map[string]float64{"2": 20.5})
	if len(got) != 3 || got[0].Key != "2025-12" || got[1].Key != "2026-01" {
		t.Fatalf("month buckets: %+v", got)
	}
	if got[1].Orders != 2 || got[1].Items != 3 || got[1].Spent != 30.5 || got[1].Refunded != 20.5 || got[1].Net != 10 {
		t.Errorf("2026-01: %+v", got[1])
	}
	got = Spend(orders, "account", nil)
	if len(got) != 2 || got[0].Key != "home" || got[0].Spent != 35.5 || got[1].Spent != 100 {
		t.Errorf("account buckets: %+v", got)
	}
	if got := Spend(orders, "year", nil); len(got) != 2 || got[1].Net != 130.5 {
		t.Errorf("year buckets: %+v", got)
	}
}

const revolutCSV = `Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance
CARD_PAYMENT,Current,2026-08-19 10:00:00,2026-08-20 09:00:00,Amazon.es,-49.00,0.00,EUR,COMPLETED,100
CARD_REFUND,Current,2026-08-21 10:00:00,2026-08-21 10:00:00,AMZN Mktp ES,99.99,0.00,EUR,COMPLETED,200
CARD_PAYMENT,Current,2026-08-22 10:00:00,2026-08-22 10:00:00,Mercadona,-12.00,0.00,EUR,COMPLETED,188
CARD_PAYMENT,Current,2026-08-23 10:00:00,2026-08-23 10:00:00,Amazon.es,-49.00,0.00,EUR,COMPLETED,139
`

const n26CSV = `"Booking Date","Value Date","Partner Name","Partner Iban","Type","Payment Reference","Account Name","Amount (EUR)","Original Amount","Original Currency","Exchange Rate"
"2026-08-20","2026-08-20","Amazon EU Sarl","","MasterCard Payment","","Main","-49.0","","",""
"2026-08-21","2026-08-21","Bakery","","MasterCard Payment","","Main","-3.5","","",""
`

func TestStatement(t *testing.T) {
	rows, src, err := Statement(strings.NewReader(revolutCSV))
	if err != nil || src != "revolut" {
		t.Fatal(src, err)
	}
	if len(rows) != 3 || rows[0].Amount != -49 || rows[0].Date != "2026-08-20" || rows[1].Amount != 99.99 {
		t.Errorf("revolut rows: %+v", rows)
	}
	rows, src, err = Statement(strings.NewReader(n26CSV))
	if err != nil || src != "n26" || len(rows) != 1 || rows[0].Amount != -49 || rows[0].Description != "Amazon EU Sarl" {
		t.Errorf("n26: %s %v %+v", src, err, rows)
	}
	if _, _, err := Statement(strings.NewReader("a,b\n1,2\n")); err == nil {
		t.Error("unknown layout must fail loudly")
	}
}

func tx(date, amount string, refund bool) store.Transaction {
	n, _ := amazon.ParseAmount(amount)
	if refund && n < 0 {
		n = -n
	}
	return store.Transaction{Transaction: amazon.Transaction{DateISO: date, Amount: amount, Refund: refund, OrderIDs: []string{"402-0000001-0000001"}}, AmountN: n}
}

func TestReconcile(t *testing.T) {
	bank, _, _ := Statement(strings.NewReader(revolutCSV))
	txs := []store.Transaction{
		tx("2026-08-19", "-€49.00", false),
		tx("2026-08-21", "+€99.99", true),
		tx("2026-08-22", "+€5.00", true),
		tx("2026-05-01", "-€7.00", false),
	}
	matches, unmatched := Reconcile(bank, txs, 4)
	states := map[string]int{}
	for _, m := range matches {
		states[m.State]++
	}
	if states["matched"] != 2 {
		t.Errorf("want 2 matched, got %v", states)
	}
	if states["charge on the statement with no Amazon charge (double charge?)"] != 1 {
		t.Errorf("second 49.00 charge should read as a double charge: %v", states)
	}
	if len(unmatched) != 1 || unmatched[0].AmountN != 5 {
		t.Errorf("the 5.00 refund never landed and the May charge is outside the period: %+v", unmatched)
	}
}

func TestQuarterAndExport(t *testing.T) {
	q, err := ParseQuarter("2026Q3")
	if err != nil || q.Year != 2026 || q.Q != 3 {
		t.Fatal(q, err)
	}
	if s, e := q.Range(); s != "2026-07-01" || e != "2026-09-31" {
		t.Errorf("range = %s..%s", s, e)
	}
	if _, err := ParseQuarter("Q5"); err == nil {
		t.Error("bad quarter accepted")
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "2026-08-10_402-0000001-0000001_invoice-1.pdf"), []byte("%PDF-1"), 0o600)
	os.WriteFile(filepath.Join(src, "2026-08-10_402-0000001-0000001_summary.pdf"), []byte("%PDF-2"), 0o600)
	os.WriteFile(filepath.Join(src, "2026-08-12_402-0000002-0000002_summary.pdf"), []byte("%PDF-3"), 0o600)
	orders := []store.Order{
		order("work", "es", "402-0000001-0000001", "2026-08-10", "€10.00", 1),
		order("work", "es", "402-0000002-0000002", "2026-08-12", "€20.00", 1),
		order("work", "es", "402-0000003-0000003", "2026-06-30", "€30.00", 1),
	}
	root := t.TempDir()
	got, dest, err := Export(orders, q, []string{src}, root, "work", true)
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(root, "2026", "Q3", "Amazon", "work") {
		t.Errorf("dest = %s", dest)
	}
	if len(got) != 2 {
		t.Fatalf("exported %d orders, want 2 (the June order is Q2): %+v", len(got), got)
	}
	if got[0].Missing || len(got[0].Files) != 1 || filepath.Base(got[0].Files[0]) != "2026-08-10_402-0000001-0000001_invoice-1.pdf" {
		t.Errorf("first: %+v", got[0])
	}
	if !got[1].Missing || len(got[1].Files) != 1 || !strings.HasSuffix(got[1].Files[0], "_NOT-AN-INVOICE.pdf") {
		t.Errorf("second should fall back to the summary, marked: %+v", got[1])
	}
	raw, _ := os.ReadFile(got[0].Files[0])
	if string(raw) != "%PDF-1" {
		t.Error("copied content differs")
	}
	again, _, _ := Export(orders, q, []string{src}, root, "work", false)
	if len(again[1].Files) != 0 || !again[1].Missing {
		t.Errorf("without summaries the missing order gets no file: %+v", again[1])
	}
}
