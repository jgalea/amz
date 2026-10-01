package price

import (
	"errors"
	"math"
	"strings"
	"sync"

	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/fx"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/product"
)

// Listing is one search result with its price converted to the base
// currency.
type Listing struct {
	product.SearchResult
	Market   string  `json:"market"`
	PriceEUR float64 `json:"price_eur,omitempty"`
	ExVATEUR float64 `json:"price_ex_vat_eur,omitempty"`
	ListEUR  float64 `json:"list_eur,omitempty"`
}

// StoreSearch is one storefront's search page after filtering. Found and
// Sponsored count what the page held before the filters, so a short list
// can be told from a walled or empty page.
type StoreSearch struct {
	Market      string    `json:"market"`
	Country     string    `json:"country"`
	URL         string    `json:"url"`
	Destination string    `json:"delivery_destination,omitempty"`
	Found       int       `json:"found"`
	Sponsored   int       `json:"sponsored"`
	Listings    []Listing `json:"listings"`
	Error       string    `json:"error,omitempty"`
	Warnings    []string  `json:"warnings,omitempty"`
}

type SearchResults struct {
	Query   string        `json:"query"`
	Base    string        `json:"base_currency"`
	FXError string        `json:"fx_error,omitempty"`
	Stores  []StoreSearch `json:"stores"`
}

type SearchOptions struct {
	Refresh bool
	Base    string
	// Country is where the buyer takes delivery, as in Options.
	Country string
	// Limit caps the listings per storefront; 0 keeps the whole page.
	Limit int
	// Brand keeps only listings whose title or brand line carries the word.
	Brand       string
	NoSponsored bool
}

// Search runs the query on every storefront given and converts the
// prices to the base currency.
func Search(query string, sfs []market.Storefront, o SearchOptions) SearchResults {
	if o.Base == "" {
		o.Base = "EUR"
	}
	res := SearchResults{Query: query, Base: o.Base, Stores: make([]StoreSearch, len(sfs))}
	var wg sync.WaitGroup
	for i, sf := range sfs {
		wg.Add(1)
		go func(i int, sf market.Storefront) {
			defer wg.Done()
			u := fetch.SearchURL(query, sf)
			page, err := fetch.Get(u, sf, o.Refresh)
			if err != nil {
				s := StoreSearch{Market: sf.Code, Country: sf.Country, URL: u, Error: err.Error()}
				if errors.Is(err, fetch.ErrBlocked) {
					s.Error += "; Amazon walls search pages harder than product pages, so retry later or read it through --session"
				}
				res.Stores[i] = s
				return
			}
			res.Stores[i] = SearchFromPage(sf, page.HTML, page.URL, o)
		}(i, sf)
	}
	wg.Wait()
	table, err := fx.Rates()
	if err != nil {
		res.FXError = err.Error()
	}
	for i := range res.Stores {
		convertListings(&res.Stores[i], table, o.Base)
	}
	return res
}

// SearchFromPage reads a search page fetched some other way (a signed-in
// session) and applies the filters; prices are left unconverted.
func SearchFromPage(sf market.Storefront, html, url string, o SearchOptions) StoreSearch {
	sp := product.ParseSearch(html, sf.Code, url, sf.Currency)
	s := StoreSearch{Market: sf.Code, Country: sf.Country, URL: url, Destination: sp.DeliveryDestination, Found: len(sp.Results), Warnings: sp.Warnings}
	if w := destinationWarning(sp.DeliveryDestination, sp.DestinationType, sf, o.Country); w != "" {
		s.Warnings = append(s.Warnings, w)
	}
	brand := strings.ToLower(strings.TrimSpace(o.Brand))
	for _, r := range sp.Results {
		if r.Sponsored {
			s.Sponsored++
			if o.NoSponsored {
				continue
			}
		}
		if brand != "" && !strings.Contains(strings.ToLower(r.Title), brand) && !strings.Contains(strings.ToLower(r.Brand), brand) {
			continue
		}
		if o.Limit > 0 && len(s.Listings) >= o.Limit {
			continue
		}
		s.Listings = append(s.Listings, Listing{SearchResult: r, Market: sf.Code})
	}
	if s.Found > 0 && len(s.Listings) == 0 {
		s.Warnings = append(s.Warnings, "every result was dropped by the filters")
	}
	return s
}

// Convert fills the base-currency prices of a store's listings with the
// ECB table, for pages read outside Search.
func Convert(s *StoreSearch, base string) error {
	table, err := fx.Rates()
	if err != nil {
		return err
	}
	convertListings(s, table, base)
	return nil
}

func convertListings(s *StoreSearch, table fx.Table, base string) {
	if table == nil {
		return
	}
	for i := range s.Listings {
		l := &s.Listings[i]
		if l.Price > 0 {
			if v, ok := table.Convert(l.Price, l.Currency, base); ok {
				l.PriceEUR = math.Round(v*100) / 100
			}
		}
		if l.PriceExVAT > 0 {
			if v, ok := table.Convert(l.PriceExVAT, l.Currency, base); ok {
				l.ExVATEUR = math.Round(v*100) / 100
			}
		}
		if l.ListPrice > 0 {
			if v, ok := table.Convert(l.ListPrice, l.Currency, base); ok {
				l.ListEUR = math.Round(v*100) / 100
			}
		}
	}
}
