package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/fx"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/price"
	"github.com/jgalea/amz/internal/product"
	"github.com/jgalea/amz/internal/pull"
	"github.com/jgalea/amz/internal/reviews"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("price", "price of an ASIN per storefront, converted to EUR (--session for your own price)", priceCmd)
	register("history", "CamelCamelCamel price-history charts for an ASIN", historyCmd)
	register("reviews", "audit a listing's public reviews for manipulation and guideline breaches", reviewsCmd)
	register("seller", "the offers on a listing: sellers, ratings, FBA or not, outliers", sellerCmd)
	register("buywith", "which of your accounts gets the lowest effective price", buywithCmd)
	fetch.CacheDir = filepath.Join(config.Dir(), "cache")
	fx.CacheDir = fetch.CacheDir
}

var asinRe = regexp.MustCompile(`(?i)\b(B0[A-Z0-9]{8})\b|/(?:dp|gp/product)/([A-Z0-9]{10})`)

// asinArg accepts a bare ASIN or any Amazon URL holding one.
func asinArg(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("needs an ASIN or product URL")
	}
	m := asinRe.FindStringSubmatch(strings.ToUpper(args[0]))
	if m == nil {
		return "", fmt.Errorf("no ASIN in %q", args[0])
	}
	if m[1] != "" {
		return m[1], nil
	}
	return m[2], nil
}

// buyerCountry is where the selected account takes delivery, or ""
// when no account is set up, in which case each storefront's own
// country is the reference.
func buyerCountry(c *common) string {
	if c.account == "all" {
		// Every account delivering to the same country is as good as one.
		list, err := config.Accounts()
		if err != nil || len(list) == 0 {
			return ""
		}
		for _, a := range list[1:] {
			if a.Country != list[0].Country {
				return ""
			}
		}
		return list[0].Country
	}
	if a, err := config.Resolve(c.account); err == nil && a.Exists() {
		return a.Country
	}
	return ""
}

// marketsFlag resolves --markets (a list, all, eu) or falls back to
// --market or the default sweep.
func marketsFlag(c *common, markets string) ([]market.Storefront, error) {
	if markets == "" && c.market != "" && c.market != "all" {
		markets = c.market
	}
	if markets == "" && c.market == "all" {
		markets = "all"
	}
	return market.Resolve(markets)
}

func priceCmd(args []string) error {
	c := flags("price", "ASIN|URL [--markets es,de,fr | all] [--match] [--session] [--refresh] [--base EUR]")
	var markets, base string
	var match, session, refresh, nosave bool
	c.fs.StringVar(&markets, "markets", "", "storefronts to check (default es,de,fr,it,nl,co.uk)")
	c.fs.StringVar(&base, "base", "EUR", "currency to convert to")
	c.fs.BoolVar(&match, "match", false, "on storefronts that do not list the ASIN, search by EAN (then title and brand)")
	c.fs.BoolVar(&session, "session", false, "also read the price your signed-in account is shown (one page load per account and storefront)")
	c.fs.BoolVar(&refresh, "refresh", false, "bypass the page cache")
	c.fs.BoolVar(&nosave, "no-save", false, "do not record the prices in the store")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	sfs, err := marketsFlag(c, markets)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	cmp := price.Compare(asin, sfs, price.Options{Refresh: refresh, Match: match, Base: strings.ToUpper(base), Country: buyerCountry(c)})

	var mine []price.AccountQuote
	if session {
		mine, err = sessionQuotes(c, asin, sfs, cmp.Base)
		if err != nil {
			return err
		}
		mine = price.Rank(mine, true)
	}
	if !nosave {
		if err := savePrices(cmp, mine); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not record prices: "+err.Error())
		}
	}
	if c.asJSON {
		return emitJSON(map[string]any{"comparison": cmp, "accounts": mine})
	}
	printComparison(cmp)
	if len(mine) > 0 {
		fmt.Println()
		fmt.Println("Your accounts:")
		printAccountQuotes(mine)
	}
	if cmp.Priced == 0 {
		return fmt.Errorf("no storefront returned a price")
	}
	return nil
}

func printComparison(cmp price.Comparison) {
	title := ""
	for _, q := range cmp.Quotes {
		if q.Title != "" {
			title = q.Title
			break
		}
	}
	fmt.Printf("%s  %s\n", cmp.ASIN, truncate(title, 90))
	if cmp.EAN != "" {
		fmt.Printf("EAN %s\n", cmp.EAN)
	}
	fmt.Printf("%-6s %-10s %10s %10s %10s  %-14s %s\n", "STORE", "ASIN", "PRICE", cmp.Base, "USED", "DELIVER TO", "NOTES")
	for _, q := range cmp.Quotes {
		if q.Error != "" && q.Price == 0 {
			fmt.Printf("%-6s %-10s %10s %10s %10s  %-14s %s\n", q.Market, q.ASIN, "-", "-", "-", "", q.Error)
			continue
		}
		used := "-"
		if q.UsedEUR > 0 {
			used = fmt.Sprintf("%.2f", q.UsedEUR)
		}
		var notes []string
		if q.Matched != "" {
			notes = append(notes, "matched by "+q.Matched)
		}
		if q.Prime {
			notes = append(notes, "prime")
		}
		if q.Coupon != "" {
			notes = append(notes, "coupon: "+truncate(q.Coupon, 40))
		}
		if q.Subscribe > 0 {
			notes = append(notes, fmt.Sprintf("S&S %.2f", q.Subscribe))
		}
		if len(q.Tiers) > 0 {
			notes = append(notes, "qty tiers: "+truncate(strings.Join(q.Tiers, "; "), 50))
		}
		if q.ImportFees != "" {
			notes = append(notes, "import: "+truncate(q.ImportFees, 50))
		}
		if q.Availability != "" {
			notes = append(notes, truncate(q.Availability, 30))
		}
		for _, w := range q.Warnings {
			notes = append(notes, "! "+truncate(w, 60))
		}
		eur := "-"
		if q.PriceEUR > 0 {
			eur = fmt.Sprintf("%.2f", q.PriceEUR)
		}
		fmt.Printf("%-6s %-10s %7.2f %-3s %10s %10s  %-14s %s\n", q.Market, q.ASIN, q.Price, q.Currency, eur, used, truncate(q.Destination, 14), strings.Join(notes, "; "))
	}
	if cmp.Cheapest != "" {
		fmt.Printf("Cheapest: amazon.%s", cmp.Cheapest)
		if cmp.Spread > 0 {
			fmt.Printf(" (spread %.1f%% across %d priced storefronts)", cmp.Spread, cmp.Priced)
		}
		fmt.Println()
	}
	if cmp.FXError != "" {
		fmt.Println("! " + cmp.FXError)
	}
	fmt.Println("Listed prices include each country's VAT; delivery to your address and import charges are on top. The DELIVER TO column is where Amazon priced the page for.")
}

// sessionQuotes reads the product page through each selected account's
// signed-in browser, on the storefronts that account uses.
func sessionQuotes(c *common, asin string, sfs []market.Storefront, base string) ([]price.AccountQuote, error) {
	accts, err := c.accounts()
	if err != nil {
		return nil, err
	}
	table, fxErr := fx.Rates()
	var out []price.AccountQuote
	for _, a := range accts {
		for _, sf := range sfs {
			if !a.HasStorefront(sf.Code) {
				continue
			}
			aq := price.AccountQuote{Account: a.Name, Type: a.Type, Market: sf.Code}
			html, u, err := pull.SessionPage(a, fetch.DetailURL(asin, sf), !c.show)
			if err != nil {
				aq.Error = err.Error()
				if err == amazon.ErrSignedOut {
					aq.Error = "signed out"
				}
				out = append(out, aq)
				continue
			}
			q := price.FromPage(asin, sf, html, u, a.Country)
			aq.Price, aq.Currency, aq.Prime = q.Price, q.Currency, q.Prime
			if q.Product != nil {
				aq.ExVAT = q.Product.PriceExVAT
			}
			if table != nil {
				if v, ok := table.Convert(aq.Price, aq.Currency, base); ok {
					aq.PriceEUR = math.Round(v*100) / 100
				}
				if v, ok := table.Convert(aq.ExVAT, aq.Currency, base); ok && aq.ExVAT > 0 {
					aq.ExVATEUR = math.Round(v*100) / 100
				}
			} else if fxErr != nil {
				aq.Error = fxErr.Error()
			}
			if q.Error != "" {
				aq.Error = q.Error
			}
			out = append(out, aq)
			time.Sleep(amazon.Delay)
		}
	}
	return out, nil
}

func printAccountQuotes(qs []price.AccountQuote) {
	fmt.Printf("  %-12s %-9s %-6s %10s %10s %10s  %s\n", "ACCOUNT", "TYPE", "STORE", "PRICE", "EX-VAT", "EFFECTIVE", "NOTE")
	for _, q := range qs {
		if q.Error != "" {
			fmt.Printf("  %-12s %-9s %-6s %10s %10s %10s  %s\n", q.Account, q.Type, q.Market, "-", "-", "-", q.Error)
			continue
		}
		ex := "-"
		if q.ExVATEUR > 0 {
			ex = fmt.Sprintf("%.2f", q.ExVATEUR)
		}
		note := q.Note
		if q.Prime {
			note = strings.TrimSpace("prime " + note)
		}
		fmt.Printf("  %-12s %-9s %-6s %10.2f %10s %10.2f  %s\n", q.Account, q.Type, q.Market, q.PriceEUR, ex, q.Effective, note)
	}
}

func savePrices(cmp price.Comparison, mine []price.AccountQuote) error {
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	at := time.Now().UTC().Format(time.RFC3339)
	for _, q := range cmp.Quotes {
		if q.Price == 0 {
			continue
		}
		if err := db.AddPrice(store.Price{ASIN: q.ASIN, Market: q.Market, At: at, Title: q.Title, Price: q.Price, Currency: q.Currency, PriceEUR: q.PriceEUR, ListPrice: q.ListPrice, UsedPrice: q.UsedPrice, Availability: q.Availability, Seller: q.SoldBy, ShipsFrom: q.ShipsFrom, DeliveryDest: q.Destination}); err != nil {
			return err
		}
	}
	for _, q := range mine {
		if q.Price == 0 {
			continue
		}
		if err := db.AddPrice(store.Price{ASIN: cmp.ASIN, Market: q.Market, At: at, Account: q.Account, Price: q.Price, Currency: q.Currency, PriceEUR: q.PriceEUR}); err != nil {
			return err
		}
	}
	return nil
}

func historyCmd(args []string) error {
	c := flags("history", "ASIN|URL [--markets es] [--period all|1y|6m|3m|1m] [--out DIR]")
	var markets, period, out string
	c.fs.StringVar(&markets, "markets", "es", "storefronts to chart")
	c.fs.StringVar(&period, "period", "all", "all, 1y, 6m, 3m, 1m")
	c.fs.StringVar(&out, "out", filepath.Join(config.Dir(), "charts"), "where to save the PNGs")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	sfs, err := marketsFlag(c, markets)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	type chart struct {
		Market string `json:"market"`
		Path   string `json:"path,omitempty"`
		URL    string `json:"url,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var results []chart
	saved := false
	for _, sf := range sfs {
		r := chart{Market: sf.Code}
		u, err := fetch.ChartURL(asin, sf, "amazon", period)
		if err != nil {
			r.Error = err.Error()
			results = append(results, r)
			continue
		}
		r.URL = u
		dest := filepath.Join(out, fmt.Sprintf("%s-%s-%s.png", asin, sf.Code, period))
		ok, err := fetch.Chart(asin, sf, dest, period)
		switch {
		case err != nil:
			r.Error = err.Error()
		case !ok:
			r.Error = "no history tracked for this ASIN"
		default:
			r.Path, saved = dest, true
		}
		results = append(results, r)
	}
	if c.asJSON {
		return emitJSON(map[string]any{"asin": asin, "charts": results})
	}
	for _, r := range results {
		if r.Path != "" {
			fmt.Printf("saved   %-6s %s\n", r.Market, r.Path)
		} else {
			fmt.Printf("skipped %-6s %s\n", r.Market, r.Error)
		}
	}
	if !saved {
		return fmt.Errorf("no chart saved")
	}
	fmt.Println("The chart legend carries lowest, highest and current price with dates; read the PNG to get them.")
	return nil
}

func reviewsCmd(args []string) error {
	c := flags("reviews", "ASIN|URL [--market es] [--refresh]")
	var refresh bool
	c.fs.BoolVar(&refresh, "refresh", false, "bypass the page cache")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	code := c.market
	if code == "" || code == "all" {
		code = "es"
	}
	sf, err := market.Get(code)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	page, err := fetch.Detail(asin, sf, refresh)
	if err != nil {
		return err
	}
	p := product.Parse(page.HTML, asin, sf.Code, page.URL, sf.Currency)
	rs := product.ParseReviews(page.HTML)
	auth := reviews.Analyse(p, rs.Reviews)
	findings := reviews.Check(rs.Reviews)
	prose := reviews.AnalyseText(rs.Reviews)
	if c.asJSON {
		machine := 0
		for _, f := range prose {
			if f.MachineLeaning {
				machine++
			}
		}
		return emitJSON(map[string]any{
			"product": p,
			"sample": map[string]any{
				"reviews_parsed": len(rs.Reviews),
				"warnings":       rs.Warnings,
				"note":           "Amazon renders roughly a dozen reviews on a public detail page. The paginated review endpoints require sign-in and are not fetched. This sample is Amazon's selection, not a random one, so it is evidence about the listing rather than a measurement of every review on it.",
			},
			"authenticity":       auth,
			"guideline_findings": findings,
			"prose":              map[string]any{"reviews_analysed": len(prose), "machine_leaning": machine, "features": prose},
			"reviews":            rs.Reviews,
			"source":             map[string]any{"url": page.URL, "from_cache": page.FromCache},
		})
	}
	printReviewAudit(p, rs, auth, findings, prose)
	if len(rs.Reviews) == 0 {
		return fmt.Errorf("no reviews parsed: an unknown result, not a clean one")
	}
	return nil
}

func printReviewAudit(p product.Product, rs product.ReviewSet, auth reviews.Authenticity, findings []reviews.Finding, prose []reviews.TextFeatures) {
	fmt.Printf("%s  (%s)  %s\n", p.ASIN, p.Market, truncate(p.Title, 80))
	if p.Brand != "" {
		fmt.Printf("Brand: %s\n", p.Brand)
	}
	if p.Price > 0 {
		fmt.Printf("Price: %.2f %s\n", p.Price, p.Currency)
	}
	if p.Rating > 0 {
		fmt.Printf("Rating: %.1f from %d ratings\n", p.Rating, p.RatingCount)
	}
	if len(p.Histogram) > 0 {
		fmt.Printf("Spread: 5star %d%%  4star %d%%  3star %d%%  2star %d%%  1star %d%%\n", p.Histogram[5], p.Histogram[4], p.Histogram[3], p.Histogram[2], p.Histogram[1])
	}
	for _, w := range p.Warnings {
		fmt.Println("  warning: " + w)
	}
	if len(rs.Reviews) == 0 {
		fmt.Println("No reviews were parsed. Nothing below has been evaluated; this is an unknown result, not a clean one.")
		for _, w := range rs.Warnings {
			fmt.Println("  warning: " + w)
		}
		return
	}
	fmt.Printf("\nSample: %d reviews rendered on the public detail page. Amazon chose them, so treat this as evidence about the listing, not a measurement of every review.\n", len(rs.Reviews))
	for _, w := range rs.Warnings {
		fmt.Println("  warning: " + w)
	}
	fmt.Println("\nAuthenticity signals")
	for _, s := range auth.Signals {
		state := "ok"
		if !s.Evaluated {
			state = "not evaluated"
		} else if s.Suspicious {
			state = "SUSPICIOUS"
		}
		val := "-"
		if s.Evaluated {
			val = fmt.Sprintf("%.3g", s.Value)
		}
		fmt.Printf("  %-14s %-32s %8s  n=%-5d %s\n", state, s.Label, val, s.N, s.Note)
	}
	for _, n := range auth.Notes {
		fmt.Println("  note: " + n)
	}
	fmt.Printf("  %d suspicious of %d evaluated\n", auth.Suspicious, auth.Scored)

	fmt.Printf("\nCommunity Guidelines candidates (%d)\n", len(findings))
	for _, f := range findings {
		fmt.Printf("  [%s/%s] %s: %q\n      %s\n", f.Severity, f.Confidence, f.RuleTitle, f.Matched, truncate(f.Excerpt, 110))
	}
	machine := 0
	for _, f := range prose {
		if f.MachineLeaning {
			machine++
		}
	}
	fmt.Printf("\nProse: %d of %d reviews lean machine-written\n", machine, len(prose))
	for _, f := range prose {
		if f.MachineLeaning {
			fmt.Printf("  %s: %s\n", f.ReviewID, strings.Join(f.Reasons, "; "))
		}
	}
	fmt.Println("\nNo trust score on purpose: the signals are the evidence, the judgment is yours.")
}

func sellerCmd(args []string) error {
	c := flags("seller", "ASIN|URL [--market es] [--used] [--session] [--refresh]")
	var used, session, refresh bool
	c.fs.BoolVar(&used, "used", false, "include used and Warehouse offers")
	c.fs.BoolVar(&session, "session", false, "read the full offers panel through the signed-in account (Amazon does not serve it anonymously)")
	c.fs.BoolVar(&refresh, "refresh", false, "bypass the page cache")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	code := c.market
	if code == "" || code == "all" {
		code = "es"
	}
	sf, err := market.Get(code)
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	page, err := fetch.Detail(asin, sf, refresh)
	if err != nil {
		return err
	}
	p := product.Parse(page.HTML, asin, sf.Code, page.URL, sf.Currency)
	var offers []product.Offer
	if session {
		acct, err := c.one()
		if err != nil {
			return err
		}
		body, err := pull.SessionFetch(acct, fetch.DetailURL(asin, sf), product.OffersURL(sf.Base(), asin, used), !c.show)
		if err != nil {
			return err
		}
		offers = product.ParseOffers(string(body), sf.Currency)
		if len(offers) == 0 {
			return fmt.Errorf("no offers parsed from the offers panel (%d bytes); the layout may have changed", len(body))
		}
	} else if p.SoldBy != "" || p.Price > 0 {
		offers = []product.Offer{{Price: p.Price, PriceText: p.PriceText, Currency: p.Currency, Seller: p.SoldBy, ShipsFrom: p.ShipsFrom, Prime: p.Prime, Pinned: true,
			Amazon: strings.HasPrefix(strings.ToLower(p.SoldBy), "amazon"), FBA: strings.HasPrefix(strings.ToLower(p.ShipsFrom), "amazon") || strings.HasPrefix(strings.ToLower(p.SoldBy), "amazon")}}
	}
	type row struct {
		product.Offer
		Flags []string `json:"flags,omitempty"`
	}
	// The reference price is the buy box offer.
	ref := p.Price
	if ref == 0 {
		for _, o := range offers {
			if o.Pinned {
				ref = o.Price
			}
		}
	}
	var rows []row
	for _, o := range offers {
		r := row{Offer: o}
		if !o.Amazon && !o.FBA {
			r.Flags = append(r.Flags, "FBM")
		}
		if session && !o.Amazon && o.RatingText == "" {
			r.Flags = append(r.Flags, "no seller rating")
		} else if session && !o.Amazon && o.RatingCount < 50 {
			r.Flags = append(r.Flags, "new seller")
		}
		if o.RatingPct > 0 && o.RatingPct < 85 {
			r.Flags = append(r.Flags, fmt.Sprintf("low rating %d%%", o.RatingPct))
		}
		if ref > 0 && o.Price > 0 && o.Price < ref*0.8 && !o.Amazon {
			r.Flags = append(r.Flags, fmt.Sprintf("%.0f%% below the buy box", (1-o.Price/ref)*100))
			if o.RatingCount < 50 {
				r.Flags = append(r.Flags, "SUSPECT: new seller far below market")
			}
		}
		rows = append(rows, r)
	}
	out := map[string]any{"asin": asin, "market": sf.Code, "title": p.Title, "reference_price": ref, "other_offers": p.OtherOffers, "other_offers_count": p.OtherOffersCount, "other_offers_from": p.OtherOffersFrom, "offers": rows, "session": session}
	if c.asJSON {
		return emitJSON(out)
	}
	fmt.Printf("%s on amazon.%s  %s\n", asin, sf.Code, truncate(p.Title, 70))
	if len(rows) == 0 {
		fmt.Println("no buy box offer on the page" + unavailable(p))
	}
	fmt.Printf("%-10s %-30s %-6s %-14s %-24s %s\n", "PRICE", "SELLER", "SHIP", "CONDITION", "RATING", "FLAGS")
	for _, r := range rows {
		ship := "FBM"
		if r.Amazon {
			ship = "AMZ"
		} else if r.FBA {
			ship = "FBA"
		}
		rating := r.RatingText
		if r.RatingPct > 0 {
			rating = fmt.Sprintf("%d%% (%d)", r.RatingPct, r.RatingCount)
		}
		cond := r.Condition
		if cond == "" && r.Pinned {
			cond = "buy box"
		}
		fmt.Printf("%-10s %-30s %-6s %-14s %-24s %s\n", r.PriceText, truncate(r.Seller, 30), ship, truncate(cond, 14), truncate(rating, 24), strings.Join(r.Flags, ", "))
	}
	if p.OtherOffers != "" {
		fmt.Printf("Other offers: %s\n", p.OtherOffers)
	}
	if !session {
		fmt.Println("Amazon only serves the full offers panel (every seller with rating and country) to a signed-in browser; add --session to read it through your account.")
	}
	return nil
}

func unavailable(p product.Product) string {
	if p.Unavailable != "" {
		return " (" + p.Unavailable + ")"
	}
	return ""
}

func buywithCmd(args []string) error {
	c := flags("buywith", "ASIN|URL [--account all] [--market es] [--no-reclaim]")
	var noReclaim bool
	c.fs.BoolVar(&noReclaim, "no-reclaim", false, "the business account cannot reclaim VAT on this purchase")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	if c.account == "" {
		c.account = "all"
	}
	accts, err := c.accounts()
	if err != nil {
		return err
	}
	var sfs []market.Storefront
	seen := map[string]bool{}
	for _, a := range accts {
		list, err := c.storefronts(a)
		if err != nil {
			return err
		}
		for _, sf := range list {
			if !seen[sf.Code] {
				seen[sf.Code] = true
				sfs = append(sfs, sf)
			}
		}
	}
	fetch.MinDelay = c.delay
	cmp := price.Compare(asin, sfs, price.Options{Base: "EUR", Country: buyerCountry(c)})
	mine, err := sessionQuotes(c, asin, sfs, "EUR")
	if err != nil {
		return err
	}
	ranked := price.Rank(mine, !noReclaim)
	if err := savePrices(cmp, mine); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record prices: "+err.Error())
	}
	if c.asJSON {
		return emitJSON(map[string]any{"anonymous": cmp, "accounts": ranked})
	}
	printComparison(cmp)
	fmt.Println()
	fmt.Println("Your accounts, cheapest effective price first:")
	printAccountQuotes(ranked)
	for _, q := range ranked {
		if q.Effective > 0 {
			fmt.Printf("Buy with %s on amazon.%s at %.2f EUR effective.\n", q.Account, q.Market, q.Effective)
			break
		}
	}
	return nil
}
