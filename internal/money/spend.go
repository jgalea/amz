// Package money is what the orders cost: spend by period, the VAT
// invoices an accountant needs, and the bank statement reconciled
// against Amazon's charges and refunds.
package money

import (
	"sort"
	"strings"

	"github.com/jgalea/amz/internal/store"
)

// Bucket is one row of a spend report.
type Bucket struct {
	Key      string  `json:"key"`
	Orders   int     `json:"orders"`
	Items    int     `json:"items"`
	Spent    float64 `json:"spent"`
	Refunded float64 `json:"refunded"`
	Net      float64 `json:"net"`
	Currency string  `json:"currency"`
}

// Spend groups orders by year, month, account, market or seller. Spent
// is the order total as shown on the card; Refunded is what the audit
// ledger says came back for that order (refunded maps order id to
// amount). Cancelled orders with no total count as zero.
func Spend(orders []store.Order, by string, refunded map[string]float64) []Bucket {
	buckets := map[string]*Bucket{}
	for _, o := range orders {
		key := ""
		switch by {
		case "year":
			if len(o.DateISO) >= 4 {
				key = o.DateISO[:4]
			}
		case "month":
			if len(o.DateISO) >= 7 {
				key = o.DateISO[:7]
			}
		case "account":
			key = o.Account
		case "market":
			key = o.Market
		case "account-market", "account/market":
			key = o.Account + "/" + o.Market
		default:
			key = "all"
		}
		if key == "" {
			key = "undated"
		}
		b := buckets[key]
		if b == nil {
			b = &Bucket{Key: key, Currency: o.Currency}
			buckets[key] = b
		}
		if b.Currency != o.Currency && o.Currency != "" {
			b.Currency = "mixed"
		}
		b.Orders++
		b.Items += len(o.Items)
		b.Spent += o.TotalN
		b.Refunded += refunded[o.ID]
	}
	var out []Bucket
	for _, b := range buckets {
		b.Net = round2(b.Spent - b.Refunded)
		b.Spent, b.Refunded = round2(b.Spent), round2(b.Refunded)
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].Key, out[j].Key) < 0 })
	return out
}

func round2(f float64) float64 {
	if f < 0 {
		return float64(int(f*100-0.5)) / 100
	}
	return float64(int(f*100+0.5)) / 100
}
