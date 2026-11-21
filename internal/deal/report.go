package deal

import (
	"fmt"
	"math"
	"strings"
)

type Observed struct {
	Observations int      `json:"observations"`
	Since        *string  `json:"since"`
	MinEUR       *float64 `json:"min_eur"`
	MaxEUR       *float64 `json:"max_eur"`
}

type Account struct {
	Account  string   `json:"account"`
	PriceEUR *float64 `json:"price_eur"`
	ExVATEUR *float64 `json:"ex_vat_eur"`
	Error    string   `json:"error,omitempty"`
}

type Store struct {
	Market              string    `json:"market"`
	URL                 string    `json:"url"`
	Title               string    `json:"title,omitempty"`
	PriceEUR            *float64  `json:"price_eur"`
	Price               *float64  `json:"price"`
	Currency            string    `json:"currency,omitempty"`
	ListPriceEUR        *float64  `json:"list_price_eur"`
	Availability        string    `json:"availability,omitempty"`
	SoldBy              string    `json:"sold_by,omitempty"`
	DeliveryDestination string    `json:"delivery_destination,omitempty"`
	Comparable          bool      `json:"comparable"`
	ComparableReason    string    `json:"comparable_reason,omitempty"`
	Low30dEUR           *float64  `json:"lowest_30d_eur"`
	Low30dReason        string    `json:"lowest_30d_reason,omitempty"`
	Camel               []Row     `json:"camel"`
	CamelReason         string    `json:"camel_reason,omitempty"`
	Low1yEUR            *float64  `json:"lowest_1y_eur"`
	High1yEUR           *float64  `json:"highest_1y_eur"`
	Low1yDate           *string   `json:"lowest_1y_date"`
	VsLow30dPct         *float64  `json:"current_vs_30d_low_pct"`
	VsLow1yPct          *float64  `json:"current_vs_1y_low_pct"`
	At1yLow             *bool     `json:"at_1y_low"`
	KeepaChart          string    `json:"keepa_chart"`
	KeepaReason         string    `json:"keepa_reason,omitempty"`
	Observed            Observed  `json:"observed"`
	Accounts            []Account `json:"accounts,omitempty"`
	Error               string    `json:"error,omitempty"`
	Warnings            []string  `json:"warnings,omitempty"`
}

type Verdict struct {
	CheapestStore      *string  `json:"cheapest_store"`
	CheapestReason     string   `json:"cheapest_reason,omitempty"`
	CurrentVs30dLowPct *float64 `json:"current_vs_30d_low_pct"`
	CurrentVs1yLowPct  *float64 `json:"current_vs_1y_low_pct"`
	At1yLow            *bool    `json:"at_1y_low"`
	Summary            string   `json:"summary"`
}

type Report struct {
	ASIN               string  `json:"asin"`
	DestinationCountry string  `json:"destination_country,omitempty"`
	CheckedAt          string  `json:"checked_at"`
	Verdict            Verdict `json:"verdict"`
	Stores             []Store `json:"stores"`
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

func ptr[T any](v T) *T { return &v }

func pct(cur, ref float64) *float64 {
	if ref <= 0 || cur <= 0 {
		return nil
	}
	return ptr(round((cur-ref)/ref*100, 1))
}

// Derive fills a store's history-based signals from its price and the
// Camel rows. A signal that cannot be computed stays nil.
func Derive(s *Store) {
	if s.Low30dEUR != nil && s.PriceEUR != nil {
		s.VsLow30dPct = pct(*s.PriceEUR, *s.Low30dEUR)
	}
	var eur []Row
	for _, r := range s.Camel {
		if r.Currency == "EUR" {
			eur = append(eur, r)
		}
	}
	if len(eur) == 0 {
		if len(s.Camel) > 0 && s.CamelReason == "" {
			s.CamelReason = "Camel prices are not in EUR"
		}
		return
	}
	low, high := eur[0].Lowest, eur[0].Highest
	for _, r := range eur[1:] {
		if r.Lowest.Price < low.Price {
			low = r.Lowest
		}
		if r.Highest.Price > high.Price {
			high = r.Highest
		}
	}
	s.Low1yEUR, s.High1yEUR, s.Low1yDate = ptr(low.Price), ptr(high.Price), ptr(low.Date)
	if s.PriceEUR != nil {
		s.VsLow1yPct = pct(*s.PriceEUR, low.Price)
		s.At1yLow = ptr(*s.PriceEUR <= low.Price*1.02)
	}
}

// Decide picks the cheapest comparable store and reads the signals off it.
func Decide(r *Report) {
	best := -1
	anyPriced := false
	for i, s := range r.Stores {
		if s.PriceEUR == nil {
			continue
		}
		anyPriced = true
		if !s.Comparable {
			continue
		}
		if best < 0 || *s.PriceEUR < *r.Stores[best].PriceEUR {
			best = i
		}
	}
	v := &r.Verdict
	switch {
	case best < 0 && !anyPriced:
		v.CheapestReason = "no storefront returned a price"
	case best < 0:
		v.CheapestReason = "no priced storefront was priced for the delivery destination"
	default:
		s := r.Stores[best]
		v.CheapestStore = ptr(s.Market)
		v.CurrentVs30dLowPct, v.CurrentVs1yLowPct, v.At1yLow = s.VsLow30dPct, s.VsLow1yPct, s.At1yLow
	}
	v.Summary = summary(r, best)
}

func summary(r *Report, best int) string {
	if best < 0 {
		return r.Verdict.CheapestReason + "; no verdict"
	}
	s := r.Stores[best]
	parts := []string{fmt.Sprintf("Cheapest is amazon.%s at EUR %.2f", s.Market, *s.PriceEUR)}
	if s.At1yLow != nil {
		if *s.At1yLow && *s.VsLow1yPct < 0 {
			parts = append(parts, fmt.Sprintf("below its previous one-year low of EUR %.2f", *s.Low1yEUR))
		} else if *s.At1yLow {
			parts = append(parts, fmt.Sprintf("at its one-year low (EUR %.2f)", *s.Low1yEUR))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f%% above its one-year low of EUR %.2f", *s.VsLow1yPct, *s.Low1yEUR))
		}
	} else {
		parts = append(parts, "one-year history unavailable")
	}
	if s.VsLow30dPct != nil {
		parts = append(parts, fmt.Sprintf("%+.1f%% against the 30-day low of EUR %.2f", *s.VsLow30dPct, *s.Low30dEUR))
	} else {
		parts = append(parts, "no 30-day low shown")
	}
	return strings.Join(parts, "; ") + "."
}
