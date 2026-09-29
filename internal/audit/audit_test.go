package audit

import (
	"testing"

	"github.com/jgalea/amz/internal/amazon"
)

func order(id, date, total, status string, returns ...string) amazon.Order {
	return amazon.Order{ID: id, DateISO: date, Total: total, Status: status, Returns: returns,
		Items: []amazon.Item{{Title: "Item for " + id}}}
}

func tx(date, amount string, ids ...string) amazon.Transaction {
	t := amazon.Transaction{DateISO: date, Amount: amount, OrderIDs: ids}
	if len(ids) > 0 {
		t.OrderID = ids[0]
	}
	if amount[0] == '+' {
		t.Refund = true
		t.Status = "Refunded"
	}
	return t
}

func find(lines []Line, id string) *Line {
	for i := range lines {
		if lines[i].OrderID == id {
			return &lines[i]
		}
	}
	return nil
}

func TestRun(t *testing.T) {
	in := Input{
		Orders: []amazon.Order{
			// Charged once, refunded in two instalments: fully refunded.
			order("404-0000001-0000001", "2026-08-08", "€213.78", "Return complete", "Return complete", "Your return is complete. Your refund has been issued."),
			// Return started, charged, nothing back yet.
			order("408-0000002-0000002", "2026-08-10", "€49.00", "Return started", "Return started", "Your refund will be processed when we receive your item."),
			// Two orders on one card charge, one of them refunded: estimate.
			order("402-0000003-0000003", "2026-08-16", "€42.38", "Refunded", "Refunded"),
			order("402-0000004-0000004", "2026-08-16", "€99.99", ""),
			// Refunded with no return wording at all.
			order("402-0000005-0000005", "2026-07-01", "€15.00", "Delivered 3 July"),
			// Cancelled before any charge.
			order("402-0000006-0000006", "2026-07-02", "€20.00", "Cancelled"),
			// Older than the oldest transaction: excluded, not "no refund".
			order("402-0000007-0000007", "2023-01-01", "€10.00", "Return complete"),
			// Newer than the newest transaction.
			order("402-0000008-0000008", "2026-09-10", "€30.00", "Return started"),
			// Plain delivered order, no money back: not in the report.
			order("402-0000009-0000009", "2026-08-01", "€5.00", "Delivered 2 August", "Return or replace items", "Return window closed on 1 September"),
		},
		Transactions: []amazon.Transaction{
			tx("2026-08-08", "-€213.78", "404-0000001-0000001"),
			tx("2026-08-10", "+€26.50", "404-0000001-0000001"),
			tx("2026-08-19", "+€184.33", "404-0000001-0000001"),
			tx("2026-08-10", "-€49.00", "408-0000002-0000002"),
			tx("2026-08-16", "-€142.37", "402-0000003-0000003", "402-0000004-0000004"),
			tx("2026-08-21", "+€42.38", "402-0000003-0000003"),
			tx("2026-07-01", "-€15.00", "402-0000005-0000005"),
			tx("2026-07-05", "+€15.00", "402-0000005-0000005"),
			tx("2026-08-01", "-€5.00", "402-0000009-0000009"),
			tx("2024-04-12", "-€1.00", "402-0000000-0000000"),
		},
	}
	rep := Run(in)

	if rep.Oldest != "2024-04-12" || rep.Newest != "2026-08-21" {
		t.Errorf("window = %s..%s", rep.Oldest, rep.Newest)
	}
	if rep.Excluded != 1 {
		t.Errorf("excluded = %d", rep.Excluded)
	}

	bosch := find(rep.Returned, "404-0000001-0000001")
	if bosch == nil || bosch.Verdict != Refunded || bosch.Refunded != 210.83 || bosch.Charged != 213.78 || bosch.Gap != 2.95 {
		t.Errorf("two-instalment refund = %+v", bosch)
	}
	if bosch != nil && bosch.Verdict == Refunded && bosch.Gap != 2.95 {
		t.Errorf("gap should still be reported")
	}

	cressi := find(rep.Returned, "408-0000002-0000002")
	if cressi == nil || cressi.Verdict != NoRefund || cressi.Charged != 49 || cressi.Refunded != 0 {
		t.Errorf("no refund = %+v", cressi)
	}

	split := find(rep.Returned, "402-0000003-0000003")
	if split == nil || !split.Estimated || split.Charged != 71.19 || split.Refunded != 42.38 || split.Verdict != Partial {
		t.Errorf("split charge = %+v", split)
	}
	if other := find(rep.Returned, "402-0000004-0000004"); other != nil {
		t.Errorf("unreturned half of a split charge should not be listed: %+v", other)
	}

	unmarked := find(rep.Unmarked, "402-0000005-0000005")
	if unmarked == nil || unmarked.Verdict != Unmarked || unmarked.Refunded != 15 {
		t.Errorf("unmarked refund = %+v", unmarked)
	}

	cancelled := find(rep.Returned, "402-0000006-0000006")
	if cancelled == nil || cancelled.Verdict != NoTransactions {
		t.Errorf("cancelled before charge = %+v", cancelled)
	}
	if find(rep.Returned, "402-0000007-0000007") != nil {
		t.Error("order before the window must be excluded")
	}
	newer := find(rep.Returned, "402-0000008-0000008")
	if newer == nil || newer.Verdict != NewerThanData {
		t.Errorf("order after the window = %+v", newer)
	}
	if find(rep.Returned, "402-0000009-0000009") != nil || find(rep.Unmarked, "402-0000009-0000009") != nil {
		t.Error("a delivered order with only the return-window line is not a return")
	}

	want := map[string]int{Refunded: 1, NoRefund: 1, Partial: 1, Unmarked: 1, NoTransactions: 1, NewerThanData: 1}
	for k, v := range want {
		if rep.Counts[k] != v {
			t.Errorf("counts[%s] = %d, want %d (all: %v)", k, rep.Counts[k], v, rep.Counts)
		}
	}
}

func TestSinceAndGiftCard(t *testing.T) {
	in := Input{
		Since: "2026-08-01",
		Orders: []amazon.Order{
			order("402-0000001-0000001", "2026-07-15", "€10.00", "Return complete"),
			order("402-0000002-0000002", "2026-08-15", "€25.00", "Return complete"),
		},
		Transactions: []amazon.Transaction{
			tx("2026-07-15", "-€10.00", "402-0000001-0000001"),
			tx("2026-08-15", "-€25.00", "402-0000002-0000002"),
		},
		GiftCard: []amazon.GiftCardEntry{
			{DateISO: "2026-08-20", Amount: "+€25.00", OrderID: "402-0000002-0000002", Description: "Refund"},
		},
	}
	rep := Run(in)
	if len(rep.Returned) != 1 || rep.Returned[0].OrderID != "402-0000002-0000002" {
		t.Fatalf("since filter: %+v", rep.Returned)
	}
	l := rep.Returned[0]
	if l.Verdict != Refunded || l.Refunded != 25 || len(l.Sources) != 2 || l.Sources[0] != "card" || l.Sources[1] != "giftcard" {
		t.Errorf("gift card refund = %+v", l)
	}
}

func TestReturnMarker(t *testing.T) {
	cases := map[string]string{
		"Return complete":                     "Return complete",
		"Delivered 16 August":                 "",
		"Cancelled":                           "Cancelled",
		"Devolución completada":               "Devolución completada",
		"Return, Replace or Withdraw":         "",
		"Return window closed on 1 September": "",
		"Your return is in transit. Your refund has been issued.": "Your return is in transit. Your refund has been issued.",
	}
	for in, want := range cases {
		if got := returnMarker(amazon.Order{Returns: []string{in}}); got != want {
			t.Errorf("returnMarker(%q) = %q, want %q", in, got, want)
		}
	}
	if got := returnMarker(amazon.Order{Status: "Delivered; Refunded"}); got != "Refunded" {
		t.Errorf("status marker = %q", got)
	}
}
