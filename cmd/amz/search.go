package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/price"
	"github.com/jgalea/amz/internal/pull"
)

func init() {
	register("search", "search a storefront and list the results with prices in EUR (--session for what your account is shown)", searchCmd)
}

// searchReady marks a rendered search page, or Amazon's no-results page.
const searchReady = "div[data-component-type='s-search-result'], .s-no-results, #noResultsTitle"

// accountSearch is one account's view of a storefront's search page.
type accountSearch struct {
	Account string `json:"account"`
	Type    string `json:"type"`
	price.StoreSearch
}

func searchCmd(args []string) error {
	c := flags("search", "QUERY [--market es] [--markets es,de,fr | all] [--limit N] [--brand X] [--no-sponsored] [--session] [--refresh] [--base EUR]")
	var markets, base, brand string
	var limit int
	var noSponsored, session, refresh bool
	c.fs.StringVar(&markets, "markets", "", "storefronts to search (default es)")
	c.fs.StringVar(&base, "base", "EUR", "currency to convert to")
	c.fs.IntVar(&limit, "limit", 20, "at most this many results per storefront (0 for the whole first page)")
	c.fs.StringVar(&brand, "brand", "", "only results whose title carries this brand")
	c.fs.BoolVar(&noSponsored, "no-sponsored", false, "drop sponsored results instead of flagging them")
	c.fs.BoolVar(&session, "session", false, "also read the results your signed-in account is shown (one page load per account and storefront)")
	c.fs.BoolVar(&refresh, "refresh", false, "bypass the page cache")
	if err := c.parse(args); err != nil {
		return err
	}
	query := strings.Join(c.args, " ")
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("needs a search query")
	}
	if limit < 0 {
		return fmt.Errorf("--limit must not be negative")
	}
	if markets == "" && c.market == "" {
		markets = "es"
	}
	sfs, err := marketsFlag(c, markets)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	o := price.SearchOptions{Refresh: refresh, Base: strings.ToUpper(base), Country: buyerCountry(c), Limit: limit, Brand: brand, NoSponsored: noSponsored}
	res := price.Search(query, sfs, o)

	var mine []accountSearch
	if session {
		mine, err = sessionSearches(c, query, sfs, o)
		if err != nil {
			return err
		}
	}
	if c.asJSON {
		return emitJSON(map[string]any{"search": res, "accounts": mine})
	}
	if c.asCSV {
		return writeSearchCSV(res, mine)
	}
	printSearch(res)
	for _, a := range mine {
		fmt.Println()
		fmt.Printf("Account %s (%s):\n", a.Account, a.Type)
		printStoreSearch(res.Base, a.StoreSearch)
	}
	listed := 0
	for _, s := range res.Stores {
		listed += len(s.Listings)
	}
	if listed == 0 {
		return fmt.Errorf("no results listed")
	}
	return nil
}

func printSearch(res price.SearchResults) {
	fmt.Printf("%q\n", res.Query)
	for _, s := range res.Stores {
		printStoreSearch(res.Base, s)
	}
	if res.FXError != "" {
		fmt.Println("! " + res.FXError)
	}
	fmt.Println("Listed prices include each country's VAT; delivery and import charges are on top. Each storefront line says where Amazon priced the page for. Sponsored results are paid placements, not the best match.")
}

func printStoreSearch(base string, s price.StoreSearch) {
	if s.Error != "" {
		fmt.Printf("amazon.%s: %s\n", s.Market, s.Error)
		return
	}
	dest := s.Destination
	if dest == "" {
		dest = "unknown"
	}
	fmt.Printf("amazon.%s: %d results on the page, %d sponsored, %d listed, priced for delivery to %s\n", s.Market, s.Found, s.Sponsored, len(s.Listings), dest)
	for _, w := range s.Warnings {
		fmt.Println("  ! " + w)
	}
	if len(s.Listings) == 0 {
		return
	}
	exVAT := false
	for _, l := range s.Listings {
		exVAT = exVAT || l.ExVATEUR > 0
	}
	exHead, exCol := "", ""
	if exVAT {
		exHead = fmt.Sprintf("%10s ", "EX-VAT")
	}
	fmt.Printf("  %3s %-10s %10s %10s %s%-14s %-11s %s\n", "#", "ASIN", "PRICE", base, exHead, "RATING", "FLAGS", "TITLE")
	for _, l := range s.Listings {
		priceCol, eur := "-", "-"
		if l.Price > 0 {
			priceCol = fmt.Sprintf("%.2f %s", l.Price, l.Currency)
		}
		if l.PriceEUR > 0 {
			eur = fmt.Sprintf("%.2f", l.PriceEUR)
		}
		if exVAT {
			exCol = fmt.Sprintf("%10s ", "-")
			if l.ExVATEUR > 0 {
				exCol = fmt.Sprintf("%10.2f ", l.ExVATEUR)
			}
		}
		rating := "-"
		if l.Rating > 0 {
			rating = fmt.Sprintf("%.1f (%d)", l.Rating, l.RatingCount)
		}
		var flags []string
		if l.Sponsored {
			flags = append(flags, "SPONSORED")
		}
		if l.Prime {
			flags = append(flags, "prime")
		}
		title := truncate(l.Title, 70)
		if l.ListEUR > 0 {
			title += fmt.Sprintf("  (list %.2f)", l.ListEUR)
		}
		fmt.Printf("  %3d %-10s %10s %10s %s%-14s %-11s %s\n", l.Position, l.ASIN, priceCol, eur, exCol, rating, strings.Join(flags, ","), title)
	}
}

func writeSearchCSV(res price.SearchResults, mine []accountSearch) error {
	w := csv.NewWriter(os.Stdout)
	w.Write([]string{"account", "market", "position", "asin", "title", "brand", "price", "currency", "price_" + strings.ToLower(res.Base), "price_ex_vat", "list_price", "rating", "rating_count", "prime", "sponsored", "delivery_destination", "url"})
	row := func(account string, s price.StoreSearch, l price.Listing) {
		w.Write([]string{account, s.Market, strconv.Itoa(l.Position), l.ASIN, l.Title, l.Brand, csvNum(l.Price), l.Currency, csvNum(l.PriceEUR), csvNum(l.PriceExVAT), csvNum(l.ListPrice), csvNum(l.Rating), strconv.Itoa(l.RatingCount), fmt.Sprint(l.Prime), fmt.Sprint(l.Sponsored), s.Destination, l.URL})
	}
	for _, s := range res.Stores {
		for _, l := range s.Listings {
			row("", s, l)
		}
	}
	for _, a := range mine {
		for _, l := range a.Listings {
			row(a.Account, a.StoreSearch, l)
		}
	}
	w.Flush()
	return w.Error()
}

func csvNum(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// sessionSearches loads the search page through each selected account's
// signed-in browser, on the storefronts that account uses.
func sessionSearches(c *common, query string, sfs []market.Storefront, o price.SearchOptions) ([]accountSearch, error) {
	accts, err := c.accounts()
	if err != nil {
		return nil, err
	}
	var out []accountSearch
	for _, a := range accts {
		for _, sf := range sfs {
			if !a.HasStorefront(sf.Code) {
				continue
			}
			as := accountSearch{Account: a.Name, Type: a.Type, StoreSearch: price.StoreSearch{Market: sf.Code, Country: sf.Country}}
			u := fetch.SearchURL(query, sf)
			html, pageURL, err := pull.SessionPage(a, u, !c.show, searchReady)
			if err != nil {
				as.Error = err.Error()
				if err == amazon.ErrSignedOut {
					as.Error = "signed out"
				}
				out = append(out, as)
				continue
			}
			as.StoreSearch = price.SearchFromPage(sf, html, pageURL, price.SearchOptions{Country: a.Country, Limit: o.Limit, Brand: o.Brand, NoSponsored: o.NoSponsored})
			if err := price.Convert(&as.StoreSearch, o.Base); err != nil {
				as.Warnings = append(as.Warnings, err.Error())
			}
			out = append(out, as)
			time.Sleep(amazon.Delay)
		}
	}
	return out, nil
}
