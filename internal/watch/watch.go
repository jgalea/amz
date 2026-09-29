// Package watch checks price watches: an ASIN, the storefronts to look
// at, and the price below which to say something.
package watch

import (
	"fmt"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/price"
	"github.com/jgalea/amz/internal/store"
)

// Outcome is one watch after a check.
type Outcome struct {
	Watch    store.Watch `json:"watch"`
	Lowest   float64     `json:"lowest_eur,omitempty"`
	Market   string      `json:"market,omitempty"`
	Used     bool        `json:"used_offer,omitempty"`
	Title    string      `json:"title,omitempty"`
	Hit      bool        `json:"hit"`
	Notified bool        `json:"notified"`
	Error    string      `json:"error,omitempty"`
}

// Renotify is how long a hit stays quiet after an alert, so the same
// low price is not announced every run.
const Renotify = 24 * time.Hour

// Check prices one watch across its storefronts and says whether it
// hit. With Used set on the watch, a used or Warehouse offer counts.
func Check(w store.Watch, country string, refresh bool) Outcome {
	out := Outcome{Watch: w}
	sfs, err := market.Resolve(w.Markets)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	cmp := price.Compare(w.ASIN, sfs, price.Options{Refresh: refresh, Country: country})
	for _, q := range cmp.Quotes {
		if q.Title != "" && out.Title == "" {
			out.Title = q.Title
		}
		if q.PriceEUR > 0 && (out.Lowest == 0 || q.PriceEUR < out.Lowest) {
			out.Lowest, out.Market, out.Used = q.PriceEUR, q.Market, false
		}
		if w.Used && q.UsedEUR > 0 && (out.Lowest == 0 || q.UsedEUR < out.Lowest) {
			out.Lowest, out.Market, out.Used = q.UsedEUR, q.Market, true
		}
	}
	if out.Lowest == 0 {
		var errs []string
		for _, q := range cmp.Quotes {
			if q.Error != "" {
				errs = append(errs, q.Market+": "+q.Error)
			}
		}
		out.Error = "no price on any storefront"
		if len(errs) > 0 {
			out.Error += " (" + strings.Join(errs, "; ") + ")"
		}
		return out
	}
	out.Hit = out.Lowest <= w.Below
	return out
}

// ShouldNotify says whether a hit deserves an alert now: it is a hit,
// and either nothing was sent before or the last alert is older than
// Renotify.
func ShouldNotify(o Outcome, now time.Time) bool {
	if !o.Hit {
		return false
	}
	if o.Watch.NotifiedAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, o.Watch.NotifiedAt)
	return err != nil || now.Sub(last) >= Renotify
}

// Message is the alert text for a hit.
func Message(o Outcome) string {
	kind := ""
	if o.Used {
		kind = " (used)"
	}
	title := o.Title
	if title == "" {
		title = o.Watch.Title
	}
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60])
	}
	return fmt.Sprintf("%s at %.2f EUR%s on amazon.%s (watching for below %.2f): https://www.amazon.%s/dp/%s", title, o.Lowest, kind, o.Market, o.Watch.Below, o.Market, o.Watch.ASIN)
}
