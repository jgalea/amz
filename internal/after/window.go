// Package after is what matters once an order is delivered: the return
// window, the legal guarantee, and returns that never turned into money.
package after

import (
	"sort"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/store"
)

// ReturnDays is Amazon's standard return window in the EU and UK,
// counted from delivery. The legal minimum is 14; Amazon gives 30.
const ReturnDays = 30

// WindowLine is one order still inside its return window.
type WindowLine struct {
	Account  string `json:"account"`
	Market   string `json:"market"`
	OrderID  string `json:"order_id"`
	Date     string `json:"date"`
	Title    string `json:"title"`
	Items    int    `json:"items"`
	Total    string `json:"total"`
	Until    string `json:"until"`
	DaysLeft int    `json:"days_left"`
	// Estimated is set when the deadline is delivery (or order) date plus
	// 30 days rather than a date the card showed.
	Estimated bool   `json:"estimated"`
	URL       string `json:"url"`
}

func hasMarker(o store.Order) bool {
	l := strings.ToLower(o.Status)
	for _, k := range []string{"return", "refund", "cancel", "devol", "reembols", "rück", "erstatt", "retour", "rembours", "reso ", "rimbors", "storn", "annul"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	for _, r := range o.Returns {
		l := strings.ToLower(r)
		for _, k := range []string{"return started", "return complete", "return received", "refunded", "devolución iniciada", "devolución completada", "reembolsado", "cancelled", "canceled", "cancelado"} {
			if strings.HasPrefix(l, k) {
				return true
			}
		}
	}
	return false
}

// Deadline is when an order stops being returnable: the card's own date
// when it showed one, else delivery plus ReturnDays, else the order
// date plus ReturnDays (goods usually arrive within days).
func Deadline(o store.Order) (string, bool) {
	if o.ReturnUntil != "" {
		return o.ReturnUntil, false
	}
	base := o.DeliveredISO
	if base == "" {
		base = o.DateISO
	}
	t, err := time.Parse("2006-01-02", base)
	if err != nil {
		return "", true
	}
	return t.AddDate(0, 0, ReturnDays).Format("2006-01-02"), true
}

// Window lists the orders whose return window is still open on the
// given day, soonest deadline first. Orders already returned, refunded
// or cancelled are left out.
func Window(orders []store.Order, today time.Time) []WindowLine {
	day := today.Format("2006-01-02")
	var out []WindowLine
	for _, o := range orders {
		if hasMarker(o) {
			continue
		}
		until, est := Deadline(o)
		if until == "" || until < day {
			continue
		}
		t, _ := time.Parse("2006-01-02", until)
		l := WindowLine{Account: o.Account, Market: o.Market, OrderID: o.ID, Date: o.DateISO, Total: o.Total, Items: len(o.Items), Until: until, Estimated: est, URL: o.URL}
		l.DaysLeft = int(t.Sub(today.Truncate(24*time.Hour)).Hours() / 24)
		if len(o.Items) > 0 {
			l.Title = o.Items[0].Title
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Until < out[j].Until })
	return out
}

// WarrantyLine is one bought item with its legal guarantee.
type WarrantyLine struct {
	Account  string `json:"account"`
	Market   string `json:"market"`
	OrderID  string `json:"order_id"`
	Date     string `json:"date"`
	ASIN     string `json:"asin,omitempty"`
	Title    string `json:"title"`
	Total    string `json:"total"`
	Country  string `json:"country"`
	Years    int    `json:"guarantee_years"`
	Until    string `json:"guarantee_until"`
	DaysLeft int    `json:"days_left"`
	Expired  bool   `json:"expired"`
	Seller   string `json:"seller,omitempty"`
	Summary  bool   `json:"order_page_saved"`
	Returned bool   `json:"returned"`
	URL      string `json:"url"`
	Invoice  string `json:"invoice_url,omitempty"`
}

// Warranty finds the items matching query (an ASIN, an order id, or
// words from the title) and works out where each stands under the
// legal guarantee of the buyer's country. sellers maps order id to the
// merchant on the payments page; summaries says which orders have a
// saved order page.
func Warranty(orders []store.Order, query, country string, sellers map[string]string, summaries map[string]bool, today time.Time) []WarrantyLine {
	q := strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(q)
	var out []WarrantyLine
	for _, o := range orders {
		for _, it := range o.Items {
			if q != "" && q != strings.ToLower(o.ID) && q != strings.ToLower(it.ASIN) && !allWords(strings.ToLower(it.Title), words) {
				continue
			}
			years := market.Guarantee(country, o.DateISO)
			l := WarrantyLine{Account: o.Account, Market: o.Market, OrderID: o.ID, Date: o.DateISO, ASIN: it.ASIN, Title: it.Title, Total: o.Total, Country: strings.ToUpper(country), Years: years, URL: o.URL, Invoice: o.InvoiceURL}
			l.Seller = sellers[o.ID]
			l.Summary = summaries[o.ID]
			l.Returned = hasMarker(o)
			if t, err := time.Parse("2006-01-02", o.DateISO); err == nil && years > 0 {
				end := t.AddDate(years, 0, 0)
				l.Until = end.Format("2006-01-02")
				l.DaysLeft = int(end.Sub(today.Truncate(24*time.Hour)).Hours() / 24)
				l.Expired = l.DaysLeft < 0
			}
			out = append(out, l)
		}
	}
	return out
}

func allWords(s string, words []string) bool {
	if len(words) == 0 {
		return true
	}
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
