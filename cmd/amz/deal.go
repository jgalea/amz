package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/deal"
	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/fx"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/price"
	"github.com/jgalea/amz/internal/product"
)

func init() {
	register("deal", "is this price a real deal, and which store is cheapest: current prices, 30-day low, one-year low, verdict as JSON", dealCmd)
}

func dealCmd(args []string) error {
	c := flags("deal", "ASIN|URL [--markets es,de,fr,it] [--account NAME] [--json]")
	var markets string
	c.fs.StringVar(&markets, "markets", "es,de,fr,it", "storefronts to compare")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	sfs, err := market.Resolve(markets)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	r := buildDeal(c, asin, sfs, c.account != "")
	if c.asJSON {
		return emitJSON(r)
	}
	printDeal(r)
	return nil
}

func eurPtr(table fx.Table, amount float64, cur string) *float64 {
	if amount <= 0 || table == nil {
		return nil
	}
	v, ok := table.Convert(amount, cur, "EUR")
	if !ok {
		return nil
	}
	r := float64(int(v*100+0.5)) / 100
	return &r
}

func fitsDestination(q price.Quote, sf market.Storefront, country, inferred string) (bool, string) {
	if q.Destination == "" {
		return false, "could not read the delivery destination, so the price is unanchored"
	}
	if q.Product != nil && q.Product.DestinationType != "COUNTRY" {
		return true, ""
	}
	d := canonCountry(q.Destination)
	if country == "" {
		if d == inferred {
			return true, ""
		}
		return false, "priced for delivery to " + q.Destination + ", unlike the other storefronts"
	}
	want := canonCountry(price.CountryName(country))
	local := want
	if s, ok := market.ByCountry(country); ok {
		local = strings.ToLower(s.CountryLocal)
	}
	if d == want || d == local || strings.Contains(d, want) {
		return true, ""
	}
	return false, "priced for delivery to " + q.Destination
}

// commonDestination is the delivery destination most priced pages agree
// on, used when no account says where the buyer is.
var countryNames = map[string]string{
	"portogallo": "portugal", "espana": "spain", "españa": "spain", "spagna": "spain", "espagne": "spain", "spanien": "spain",
	"italia": "italy", "italien": "italy", "italie": "italy", "deutschland": "germany", "germania": "germany", "allemagne": "germany",
	"francia": "france", "frankreich": "france", "frankrijk": "france", "irlanda": "ireland", "irlande": "ireland", "irland": "ireland",
	"paises bajos": "netherlands", "países bajos": "netherlands", "paesi bassi": "netherlands", "pays-bas": "netherlands", "niederlande": "netherlands",
	"belgio": "belgium", "belgique": "belgium", "belgien": "belgium", "bélgica": "belgium", "österreich": "austria", "austria": "austria", "autriche": "austria",
}

func canonCountry(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := countryNames[s]; ok {
		return c
	}
	return s
}

func commonDestination(qs []price.Quote) string {
	n := map[string]int{}
	best := ""
	for _, q := range qs {
		if q.Price == 0 || q.Destination == "" {
			continue
		}
		d := canonCountry(q.Destination)
		n[d]++
		if n[d] > n[best] {
			best = d
		}
	}
	return best
}

func buildDeal(c *common, asin string, sfs []market.Storefront, withAccount bool) deal.Report {
	country := buyerCountry(c)
	r := deal.Report{ASIN: asin, DestinationCountry: country, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	cmp := price.Compare(asin, sfs, price.Options{Refresh: true, Base: "EUR", Country: country})
	table, _ := fx.Rates()
	inferred := commonDestination(cmp.Quotes)
	if country == "" {
		r.DestinationCountry = inferred
	}

	var mine []price.AccountQuote
	var accountErr string
	if withAccount {
		var err error
		if mine, err = sessionQuotes(c, asin, sfs, "EUR"); err != nil {
			accountErr = err.Error()
		}
	}
	if err := savePrices(cmp, mine); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record prices: "+err.Error())
	}
	db, dbErr := openStore()
	if db != nil {
		defer db.Close()
	}
	chartDir := filepath.Join(config.Dir(), "charts")

	for i, q := range cmp.Quotes {
		sf := sfs[i]
		s := deal.Store{Market: sf.Code, URL: q.URL, Title: q.Title, Currency: q.Currency, Availability: q.Availability, SoldBy: q.SoldBy,
			DeliveryDestination: q.Destination, Error: q.Error, Warnings: q.Warnings}
		if q.PriceEUR > 0 && !q.ForeignPrice {
			s.PriceEUR = &q.PriceEUR
			p := q.Price
			s.Price = &p
		}
		s.ListPriceEUR = eurPtr(table, q.ListPrice, q.Currency)
		s.Comparable, s.ComparableReason = fitsDestination(q, sf, country, inferred)

		if q.Price > 0 {
			if page, err := fetch.Detail(asin, sf, false); err == nil {
				if v, cur, ok := product.Lowest30(page.HTML, sf.Currency); ok {
					s.Low30dEUR = eurPtr(table, v, cur)
					if s.Low30dEUR == nil {
						s.Low30dReason = "could not convert " + cur + " to EUR"
					}
				} else {
					s.Low30dReason = "the page shows no 30-day lowest price (Amazon only prints it beside a discount)"
				}
			} else {
				s.Low30dReason = err.Error()
			}
		} else {
			s.Low30dReason = "no price on this storefront"
		}

		cr := saveChart(asin, sf, "camel", "amazon,new", "1y", chartDir)
		if cr.Path == "" {
			s.CamelReason = cr.Error
		} else {
			s.Camel, s.CamelReason = deal.ReadLegend(cr.Path)
		}
		kr := saveChart(asin, sf, "keepa", "amazon,new", "1y", chartDir)
		s.KeepaChart, s.KeepaReason = kr.Path, kr.Error
		deal.Derive(&s)

		if db != nil {
			if rows, err := db.Prices(asin, time.Now().AddDate(-1, 0, 0)); err == nil {
				for _, p := range rows {
					if p.Account != "" || p.Market != sf.Code || p.PriceEUR <= 0 {
						continue
					}
					o := &s.Observed
					o.Observations++
					if o.MinEUR == nil || p.PriceEUR < *o.MinEUR {
						v := p.PriceEUR
						o.MinEUR = &v
					}
					if o.MaxEUR == nil || p.PriceEUR > *o.MaxEUR {
						v := p.PriceEUR
						o.MaxEUR = &v
					}
					if o.Since == nil || p.At < *o.Since {
						v := p.At
						o.Since = &v
					}
				}
			}
		} else if dbErr != nil {
			s.Warnings = append(s.Warnings, "price store unavailable: "+dbErr.Error())
		}

		for _, a := range mine {
			if a.Market != sf.Code {
				continue
			}
			acc := deal.Account{Account: a.Account, Error: a.Error}
			if a.PriceEUR > 0 {
				v := a.PriceEUR
				acc.PriceEUR = &v
			}
			if a.ExVATEUR > 0 {
				v := a.ExVATEUR
				acc.ExVATEUR = &v
			}
			s.Accounts = append(s.Accounts, acc)
		}
		if accountErr != "" {
			s.Accounts = append(s.Accounts, deal.Account{Error: accountErr})
		}
		r.Stores = append(r.Stores, s)
	}
	deal.Decide(&r)
	return r
}

func fmtEUR(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *v)
}

func fmtPct(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", *v)
}

func printDeal(r deal.Report) {
	fmt.Println(r.Verdict.Summary)
	fmt.Println()
	fmt.Printf("%-6s %9s %9s %9s %9s %8s %8s  %-12s %s\n", "STORE", "PRICE", "30D LOW", "1Y LOW", "1Y HIGH", "VS 30D", "VS 1Y", "DELIVER TO", "NOTE")
	for _, s := range r.Stores {
		note := s.Error
		if note == "" && !s.Comparable {
			note = s.ComparableReason
		}
		fmt.Printf("%-6s %9s %9s %9s %9s %8s %8s  %-12s %s\n", s.Market, fmtEUR(s.PriceEUR), fmtEUR(s.Low30dEUR), fmtEUR(s.Low1yEUR), fmtEUR(s.High1yEUR), fmtPct(s.VsLow30dPct), fmtPct(s.VsLow1yPct), s.DeliveryDestination, note)
		if s.Low1yEUR != nil && s.Low1yDate != nil {
			fmt.Printf("       one-year low on %s\n", *s.Low1yDate)
		}
		if s.Observed.Observations > 0 {
			fmt.Printf("       observed here: %d reading(s), %s to %s since %s\n", s.Observed.Observations, fmtEUR(s.Observed.MinEUR), fmtEUR(s.Observed.MaxEUR), (*s.Observed.Since)[:10])
		}
		for _, a := range s.Accounts {
			if a.Error != "" {
				fmt.Printf("       account %s: %s\n", a.Account, a.Error)
			} else {
				fmt.Printf("       account %s: %s (ex-VAT %s)\n", a.Account, fmtEUR(a.PriceEUR), fmtEUR(a.ExVATEUR))
			}
		}
	}
}
