package health

import (
	"fmt"
	"testing"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/store"
)

var today = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)

func orders(n int, daysAgo int, returned int) []store.Order {
	var out []store.Order
	for i := 0; i < n; i++ {
		o := store.Order{Account: "me", Market: "es", TotalN: 10, Order: amazon.Order{ID: fmt.Sprintf("%d-%d", daysAgo, i), DateISO: today.AddDate(0, 0, -daysAgo-i).Format("2006-01-02"), Total: "€10.00"}}
		if i < returned {
			o.Status = "Return complete"
		}
		out = append(out, o)
	}
	return out
}

func TestRun(t *testing.T) {
	// 10 recent orders, 3 returned; 40 older ones, 2 returned.
	all := append(orders(10, 1, 3), orders(40, 120, 2)...)
	rep := Run("me", "es", all, map[string]float64{"1-0": 8}, today)
	if len(rep.Windows) != 3 {
		t.Fatalf("windows: %+v", rep.Windows)
	}
	w3, w12 := rep.Windows[0], rep.Windows[2]
	if w3.Orders != 10 || w3.Returned != 3 || w3.RateByCount != 0.3 || w3.Level != "critical" {
		t.Errorf("3m: %+v", w3)
	}
	if w3.ReturnedValue != 28 {
		t.Errorf("returned value should use the refund when known (8+10+10): %v", w3.ReturnedValue)
	}
	if w12.Orders != 50 || w12.Returned != 5 || w12.RateByCount != 0.1 || w12.Level != "warn" {
		t.Errorf("12m: %+v", w12)
	}
	if rep.Trend != "rising" || rep.Level != "critical" || rep.Warning == "" {
		t.Errorf("report: trend=%s level=%s warning=%q", rep.Trend, rep.Level, rep.Warning)
	}

	calm := Run("me", "es", orders(30, 5, 1), nil, today)
	if calm.Level != "ok" || calm.Trend != "flat" || calm.Warning != "" {
		t.Errorf("calm account: %+v", calm)
	}
	empty := Run("me", "es", nil, nil, today)
	if empty.Trend != "too few recent orders to call" || empty.Level != "ok" {
		t.Errorf("empty: %+v", empty)
	}
}
