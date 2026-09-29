// Package health watches the numbers Amazon watches: how much of what
// an account buys goes back.
package health

import (
	"math"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/store"
)

// Thresholds are the return-rate levels at which accounts get warned
// and then restricted. Amazon publishes none; these are the levels
// reported by people who were, so treat them as a smoke alarm, not a
// rule.
const (
	WarnRate     = 0.10
	CriticalRate = 0.20
)

type Window struct {
	Months        int     `json:"months"`
	Orders        int     `json:"orders"`
	Returned      int     `json:"returned"`
	Spent         float64 `json:"spent"`
	ReturnedValue float64 `json:"returned_value"`
	RateByCount   float64 `json:"rate_by_count"`
	RateByValue   float64 `json:"rate_by_value"`
	Level         string  `json:"level"`
}

type Report struct {
	Account string   `json:"account"`
	Market  string   `json:"market"`
	Windows []Window `json:"windows"`
	// Trend compares the 3-month rate with the 12-month rate.
	Trend   string `json:"trend"`
	Level   string `json:"level"`
	Warning string `json:"warning,omitempty"`
}

func hasReturn(o store.Order, refunded float64) bool {
	if refunded > 0 {
		return true
	}
	for _, r := range o.Returns {
		l := strings.ToLower(r)
		for _, k := range []string{"return started", "return complete", "return received", "refunded", "replacement", "devolución", "reembols", "rücksend", "erstatt", "retour", "rembours", "reso ", "rimbors"} {
			if strings.HasPrefix(l, k) {
				return true
			}
		}
	}
	l := strings.ToLower(o.Status)
	for _, k := range []string{"return", "refund", "devol", "reembols", "rück", "erstatt", "retour", "rembours", "reso ", "rimbors"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// Run computes the rolling 3, 6 and 12 month return rates for one
// account/storefront. refunded maps order id to what came back. Orders
// cancelled before payment do not count as orders.
func Run(account, mkt string, orders []store.Order, refunded map[string]float64, today time.Time) Report {
	rep := Report{Account: account, Market: mkt}
	for _, months := range []int{3, 6, 12} {
		since := today.AddDate(0, -months, 0).Format("2006-01-02")
		w := Window{Months: months}
		for _, o := range orders {
			if o.DateISO < since || o.TotalN == 0 {
				continue
			}
			w.Orders++
			w.Spent += o.TotalN
			if hasReturn(o, refunded[o.ID]) {
				w.Returned++
				v := refunded[o.ID]
				if v == 0 {
					v = o.TotalN
				}
				w.ReturnedValue += v
			}
		}
		if w.Orders > 0 {
			w.RateByCount = round3(float64(w.Returned) / float64(w.Orders))
		}
		if w.Spent > 0 {
			w.RateByValue = round3(w.ReturnedValue / w.Spent)
		}
		w.Spent, w.ReturnedValue = round2(w.Spent), round2(w.ReturnedValue)
		w.Level = level(math.Max(w.RateByCount, w.RateByValue))
		rep.Windows = append(rep.Windows, w)
	}
	short, long := rep.Windows[0], rep.Windows[2]
	rep.Level = long.Level
	if short.Orders >= 5 && level(math.Max(short.RateByCount, short.RateByValue)) != "ok" {
		rep.Level = level(math.Max(short.RateByCount, short.RateByValue))
	}
	switch {
	case short.Orders < 5:
		rep.Trend = "too few recent orders to call"
	case short.RateByCount > long.RateByCount+0.05:
		rep.Trend = "rising"
	case short.RateByCount < long.RateByCount-0.05:
		rep.Trend = "falling"
	default:
		rep.Trend = "flat"
	}
	if rep.Level == "critical" {
		rep.Warning = "return rate is at the level where accounts get restricted; stop returning for a while"
	} else if rep.Level == "warn" {
		rep.Warning = "return rate is above the level where Amazon starts warning"
	} else if rep.Trend == "rising" {
		rep.Warning = "return rate is rising against the 12-month figure"
	}
	return rep
}

func level(rate float64) string {
	switch {
	case rate >= CriticalRate:
		return "critical"
	case rate >= WarnRate:
		return "warn"
	}
	return "ok"
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
func round2(f float64) float64 { return math.Round(f*100) / 100 }
