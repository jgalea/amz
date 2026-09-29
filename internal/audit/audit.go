// Package audit matches returned orders against the money that
// actually came back, offline, from files written by `amz dump`.
package audit

import (
	"math"
	"sort"
	"strings"

	"github.com/jgalea/amz/internal/amazon"
)

type Input struct {
	Orders       []amazon.Order
	Transactions []amazon.Transaction
	GiftCard     []amazon.GiftCardEntry
	// Summaries are saved order pages; their refund total is Amazon's
	// own figure and wins over what the payment logs could be matched to.
	Summaries map[string]amazon.Summary
	// Since drops orders placed before this date (YYYY-MM-DD).
	Since string
}

// Line is one order's money trail.
type Line struct {
	OrderID string  `json:"order_id"`
	Date    string  `json:"date"`
	Title   string  `json:"title"`
	Total   string  `json:"total"`
	TotalN  float64 `json:"total_n"`
	// Marker is the return or cancellation wording that put the order
	// in the report; empty for refunds with no such wording.
	Marker   string  `json:"marker,omitempty"`
	Charged  float64 `json:"charged"`
	Refunded float64 `json:"refunded"`
	Gap      float64 `json:"gap"`
	// Estimated is set when a charge or refund covered several orders
	// and was split evenly between them, so the figures are a guess.
	Estimated bool     `json:"estimated"`
	Sources   []string `json:"sources,omitempty"`
	Verdict   string   `json:"verdict"`
}

// Tolerance is how far short of the charge a refund may fall and still
// count as complete. Amazon deducts return shipping (a few euro) from
// many refunds; the gap is still reported, it just does not make the
// order "partial".
const Tolerance = 5.00

const (
	Refunded       = "refunded"
	Partial        = "partial"
	NoRefund       = "no refund seen"
	NoTransactions = "no transactions"
	NewerThanData  = "newer than transactions"
	Unmarked       = "refunded without return marker"
	UnmarkedPart   = "partial refund without return marker"
)

type Report struct {
	// Oldest and Newest are the coverage window of the transactions.
	Oldest string `json:"oldest_transaction"`
	Newest string `json:"newest_transaction"`
	Since  string `json:"since,omitempty"`
	// Excluded counts orders placed before the oldest transaction; a
	// refund for those could never be in the data, so they are left out
	// rather than reported as unrefunded.
	Excluded int `json:"excluded_before_window"`
	// Returned holds orders with return or cancellation wording.
	Returned []Line `json:"returned"`
	// Unmarked holds orders that got money back with no such wording,
	// which Amazon does at times; they are settled, not missing.
	Unmarked []Line         `json:"unmarked_refunds"`
	Counts   map[string]int `json:"counts"`
}

type money struct {
	charged, refunded float64
	estimated         bool
	sources           map[string]bool
}

func Run(in Input) Report {
	rep := Report{Since: in.Since, Counts: map[string]int{}}
	for _, t := range in.Transactions {
		if t.DateISO == "" {
			continue
		}
		if rep.Oldest == "" || t.DateISO < rep.Oldest {
			rep.Oldest = t.DateISO
		}
		if t.DateISO > rep.Newest {
			rep.Newest = t.DateISO
		}
	}

	ledger := map[string]*money{}
	entry := func(id string) *money {
		m := ledger[id]
		if m == nil {
			m = &money{sources: map[string]bool{}}
			ledger[id] = m
		}
		return m
	}
	for _, t := range in.Transactions {
		// The payments page repeats gift card movements as its own rows;
		// when the gift card log is loaded it is the source for those.
		if len(in.GiftCard) > 0 && strings.Contains(strings.ToLower(t.Merchant), "gift card") {
			continue
		}
		amt, ok := amazon.ParseAmount(t.Amount)
		if !ok {
			continue
		}
		ids := t.OrderIDs
		if len(ids) == 0 && t.OrderID != "" {
			ids = []string{t.OrderID}
		}
		if len(ids) == 0 {
			continue
		}
		if t.Refund && amt < 0 {
			amt = -amt
		}
		share := amt / float64(len(ids))
		for _, id := range ids {
			m := entry(id)
			m.sources["card"] = true
			if len(ids) > 1 {
				m.estimated = true
			}
			if share > 0 {
				m.refunded += share
			} else {
				m.charged -= share
			}
		}
	}
	for _, g := range in.GiftCard {
		amt, ok := amazon.ParseAmount(g.Amount)
		if !ok || g.OrderID == "" {
			continue
		}
		m := entry(g.OrderID)
		m.sources["giftcard"] = true
		if amt > 0 {
			m.refunded += amt
		} else {
			m.charged -= amt
		}
	}

	for _, o := range in.Orders {
		sum, hasSum := in.Summaries[o.ID]
		if !hasSum && o.DateISO != "" && rep.Oldest != "" && o.DateISO < rep.Oldest {
			rep.Excluded++
			continue
		}
		if in.Since != "" && o.DateISO != "" && o.DateISO < in.Since {
			continue
		}
		marker := returnMarker(o)
		m := ledger[o.ID]
		if marker == "" && (m == nil || m.refunded == 0) {
			continue
		}
		l := Line{OrderID: o.ID, Date: o.DateISO, Total: o.Total, Marker: marker}
		if l.Date == "" {
			l.Date = o.Date
		}
		if len(o.Items) > 0 {
			l.Title = o.Items[0].Title
		}
		l.TotalN, _ = amazon.ParseAmount(o.Total)
		if m != nil {
			l.Charged, l.Refunded, l.Estimated = round(m.charged), round(m.refunded), m.estimated
			for s := range m.sources {
				l.Sources = append(l.Sources, s)
			}
			sort.Strings(l.Sources)
		}
		expected := l.Charged
		if expected == 0 {
			expected = l.TotalN
		}
		if hasSum {
			if sum.RefundTotal > l.Refunded {
				l.Refunded = round(sum.RefundTotal)
			}
			if expected == 0 {
				expected = sum.Total
			}
			l.Sources = append(l.Sources, "order page")
		}
		l.Gap = round(expected - l.Refunded)
		switch {
		case hasSum && marker != "" && !sum.OpenReturn && l.Refunded > 0:
			// Every return on the order is closed and Amazon says what it
			// refunded; a gap here is items that were kept, not money owed.
			l.Verdict = Refunded
		case hasSum && marker != "" && sum.OpenReturn && l.Refunded == 0:
			l.Verdict = NoRefund
		case hasSum && marker != "" && sum.OpenReturn && l.Gap > Tolerance:
			l.Verdict = Partial
		case marker == "":
			l.Verdict = Unmarked
			if l.Gap > Tolerance {
				l.Verdict = UnmarkedPart
			}
			rep.Unmarked = append(rep.Unmarked, l)
			rep.Counts[l.Verdict]++
			continue
		case l.Refunded > 0 && l.Gap <= Tolerance:
			l.Verdict = Refunded
		case l.Refunded > 0:
			l.Verdict = Partial
		case o.DateISO != "" && rep.Newest != "" && o.DateISO > rep.Newest:
			l.Verdict = NewerThanData
		case l.Charged == 0:
			l.Verdict = NoTransactions
		default:
			l.Verdict = NoRefund
		}
		rep.Returned = append(rep.Returned, l)
		rep.Counts[l.Verdict]++
	}
	return rep
}

// markerPrefixes are the lines on an order card that mean a return,
// replacement, refund or cancellation is in play, on the EN, ES, DE,
// FR, IT and PT storefronts. Matched against the lowercased line start.
var markerPrefixes = []string{
	"return started", "return received", "return complete", "return request approved",
	"refunded", "replacement started", "replacement complete", "replacement received",
	"your return is", "your refund has been issued", "there's no need to return", "there’s no need to return",
	"we are expecting your return", "we've received your return", "we’ve received your return",
	"cancelled", "canceled",
	"devolución iniciada", "devolución recibida", "devolución completada", "devolución aprobada",
	"reembolsado", "reembolso emitido", "sustitución completada", "cancelado", "pedido cancelado",
	"rücksendung", "erstattet", "erstattung", "storniert",
	"retour ", "remboursé", "annulé",
	"reso ", "rimborsato", "annullato",
	"devolução", "reembolsado", "cancelado",
}

func returnMarker(o amazon.Order) string {
	var lines []string
	for _, s := range strings.Split(o.Status, "; ") {
		lines = append(lines, s)
	}
	lines = append(lines, o.Returns...)
	for _, line := range lines {
		l := strings.ToLower(strings.TrimSpace(line))
		for _, p := range markerPrefixes {
			if strings.HasPrefix(l, p) {
				return strings.TrimSpace(line)
			}
		}
	}
	return ""
}

func round(f float64) float64 {
	return math.Round(f*100) / 100
}
