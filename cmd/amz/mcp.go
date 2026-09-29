package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jgalea/amz/internal/after"
	"github.com/jgalea/amz/internal/audit"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/health"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/mcp"
	"github.com/jgalea/amz/internal/money"
	"github.com/jgalea/amz/internal/price"
	"github.com/jgalea/amz/internal/product"
	"github.com/jgalea/amz/internal/pull"
	"github.com/jgalea/amz/internal/reviews"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("mcp", "serve the read commands as MCP tools over stdio (for Claude and other agents)", mcpCmd)
}

const version = "0.1.0"

func mcpCmd(args []string) error {
	c := flags("mcp", "")
	if err := c.parse(args); err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	s := &mcp.Server{Name: "amz", Version: version, Tools: mcpTools()}
	fmt.Fprintln(os.Stderr, "amz mcp: serving on stdio")
	return s.Stdio(context.Background())
}

// mcpCommon builds the flag context the command helpers expect from
// tool arguments.
func mcpCommon(args map[string]any) *common {
	c := flags("mcp", "")
	c.account = mcp.String(args, "account")
	c.market = mcp.String(args, "market")
	c.delay = 2 * time.Second
	return c
}

func mcpAsin(args map[string]any) (string, error) {
	return asinArg([]string{mcp.String(args, "asin")})
}

func mcpTools() []mcp.Tool {
	asinProp := mcp.Str("ASIN or Amazon product URL")
	accountProp := mcp.Str("account name, or all (default: the only account)")
	marketProp := mcp.Str("storefront code (es, de, fr, it, nl, be, ie, pl, se, co.uk, com), or all")
	return []mcp.Tool{
		{
			Name:        "price",
			Description: "Price of a product on each Amazon storefront, converted to EUR, with availability, used offers, coupons and where Amazon priced the page for. Anonymous, paced, cached.",
			Schema:      mcp.Schema(map[string]any{"asin": asinProp, "markets": mcp.Str("comma list of storefronts, or all/eu (default es,de,fr,it,nl,co.uk)"), "match": mcp.Bool("search other storefronts by EAN/title when the ASIN is not listed"), "account": accountProp}, "asin"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				asin, err := mcpAsin(args)
				if err != nil {
					return nil, err
				}
				sfs, err := market.Resolve(mcp.String(args, "markets"))
				if err != nil {
					return nil, err
				}
				cmp := price.Compare(asin, sfs, price.Options{Match: mcp.Flag(args, "match"), Base: "EUR", Country: buyerCountry(mcpCommon(args))})
				savePrices(cmp, nil)
				return cmp, nil
			},
		},
		{
			Name:        "history",
			Description: "Download the CamelCamelCamel price-history chart for a product; the legend in the PNG carries the lowest, highest and current price with dates. Returns the file path.",
			Schema:      mcp.Schema(map[string]any{"asin": asinProp, "market": marketProp, "period": mcp.Str("all, 1y, 6m, 3m or 1m")}, "asin"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				asin, err := mcpAsin(args)
				if err != nil {
					return nil, err
				}
				code := mcp.String(args, "market")
				if code == "" {
					code = "es"
				}
				sf, err := market.Get(code)
				if err != nil {
					return nil, err
				}
				period := mcp.String(args, "period")
				if period == "" {
					period = "all"
				}
				dest := filepath.Join(config.Dir(), "charts", fmt.Sprintf("%s-%s-%s.png", asin, sf.Code, period))
				ok, err := fetch.Chart(asin, sf, dest, period)
				if err != nil {
					return nil, err
				}
				if !ok {
					return map[string]any{"asin": asin, "market": sf.Code, "error": "no history tracked for this ASIN"}, nil
				}
				return map[string]any{"asin": asin, "market": sf.Code, "path": dest, "note": "read the PNG: the legend prints lowest, highest and current price with dates"}, nil
			},
		},
		{
			Name:        "reviews",
			Description: "Audit the reviews Amazon renders on a product's public page: authenticity signals, guideline breaches, machine-written prose features, and the reviews themselves. Evidence, not a score; a signal with evaluated=false was not evaluated.",
			Schema:      mcp.Schema(map[string]any{"asin": asinProp, "market": marketProp}, "asin"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				asin, err := mcpAsin(args)
				if err != nil {
					return nil, err
				}
				code := mcp.String(args, "market")
				if code == "" {
					code = "es"
				}
				sf, err := market.Get(code)
				if err != nil {
					return nil, err
				}
				page, err := fetch.Detail(asin, sf, false)
				if err != nil {
					return nil, err
				}
				p := product.Parse(page.HTML, asin, sf.Code, page.URL, sf.Currency)
				rs := product.ParseReviews(page.HTML)
				return map[string]any{
					"product":            p,
					"reviews_parsed":     len(rs.Reviews),
					"warnings":           rs.Warnings,
					"authenticity":       reviews.Analyse(p, rs.Reviews),
					"guideline_findings": reviews.Check(rs.Reviews),
					"prose":              reviews.AnalyseText(rs.Reviews),
					"reviews":            rs.Reviews,
				}, nil
			},
		},
		{
			Name:        "seller",
			Description: "Who sells and ships a product: the buy box seller anonymously, or the full offers panel through a signed-in account (session=true), with seller ratings and flags for new sellers far below market.",
			Schema:      mcp.Schema(map[string]any{"asin": asinProp, "market": marketProp, "session": mcp.Bool("read the full offers panel through the signed-in account"), "account": accountProp}, "asin"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				asin, err := mcpAsin(args)
				if err != nil {
					return nil, err
				}
				code := mcp.String(args, "market")
				if code == "" {
					code = "es"
				}
				sf, err := market.Get(code)
				if err != nil {
					return nil, err
				}
				page, err := fetch.Detail(asin, sf, false)
				if err != nil {
					return nil, err
				}
				p := product.Parse(page.HTML, asin, sf.Code, page.URL, sf.Currency)
				out := map[string]any{"asin": asin, "market": sf.Code, "title": p.Title, "buy_box": map[string]any{"price": p.Price, "currency": p.Currency, "sold_by": p.SoldBy, "ships_from": p.ShipsFrom, "prime": p.Prime}, "other_offers": p.OtherOffers}
				if mcp.Flag(args, "session") {
					c := mcpCommon(args)
					acct, err := c.one()
					if err != nil {
						return nil, err
					}
					body, err := pull.SessionFetch(acct, fetch.DetailURL(asin, sf), product.OffersURL(sf.Base(), asin, false), true)
					if err != nil {
						return nil, err
					}
					out["offers"] = product.ParseOffers(string(body), sf.Currency)
				}
				return out, nil
			},
		},
		{
			Name:        "buywith",
			Description: "Which of the configured accounts gets the lowest effective price for a product: anonymous prices per storefront plus what each signed-in account is shown, ranked (ex-VAT for a business account that reclaims VAT).",
			Schema:      mcp.Schema(map[string]any{"asin": asinProp, "no_reclaim": mcp.Bool("the business account cannot reclaim VAT on this purchase")}, "asin"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				asin, err := mcpAsin(args)
				if err != nil {
					return nil, err
				}
				c := mcpCommon(args)
				c.account = "all"
				accts, err := c.accounts()
				if err != nil {
					return nil, err
				}
				var sfs []market.Storefront
				seen := map[string]bool{}
				for _, a := range accts {
					list, err := c.storefronts(a)
					if err != nil {
						return nil, err
					}
					for _, sf := range list {
						if !seen[sf.Code] {
							seen[sf.Code] = true
							sfs = append(sfs, sf)
						}
					}
				}
				cmp := price.Compare(asin, sfs, price.Options{Base: "EUR", Country: buyerCountry(c)})
				mine, err := sessionQuotes(c, asin, sfs, "EUR")
				if err != nil {
					return nil, err
				}
				ranked := price.Rank(mine, !mcp.Flag(args, "no_reclaim"))
				savePrices(cmp, mine)
				return map[string]any{"anonymous": cmp, "accounts": ranked}, nil
			},
		},
		{
			Name:        "orders",
			Description: "Search the stored order history (run `amz sync` to update it): by title words, ASIN, order id, date range, account and storefront.",
			Schema:      mcp.Schema(map[string]any{"grep": mcp.Str("words the item title must contain"), "asin": mcp.Str("only orders holding this ASIN"), "order_id": mcp.Str("one order"), "since": mcp.Str("YYYY-MM-DD"), "until": mcp.Str("YYYY-MM-DD"), "account": accountProp, "market": marketProp, "limit": mcp.Num("at most this many orders (default 50)")}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				if c.account == "" {
					c.account = "all"
				}
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				q, err := c.filter()
				if err != nil {
					return nil, err
				}
				q.Grep, q.ASIN, q.OrderID, q.Since, q.Until = mcp.String(args, "grep"), mcp.String(args, "asin"), mcp.String(args, "order_id"), mcp.String(args, "since"), mcp.String(args, "until")
				orders, err := db.Orders(q)
				if err != nil {
					return nil, err
				}
				limit := int(mcp.Number(args, "limit"))
				if limit <= 0 {
					limit = 50
				}
				total := len(orders)
				if len(orders) > limit {
					orders = orders[:limit]
				}
				if orders == nil {
					orders = []store.Order{}
				}
				return map[string]any{"total": total, "orders": orders}, nil
			},
		},
		{
			Name:        "warranty",
			Description: "Find a bought item and where it stands under the legal guarantee of the account's country: order, seller, order page, days left.",
			Schema:      mcp.Schema(map[string]any{"query": mcp.Str("words from the title, an ASIN or an order id"), "account": accountProp, "country": mcp.Str("two-letter country to apply instead of the account's")}, "query"),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				if c.account == "" {
					c.account = "all"
				}
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				orders, err := allOrders(db, c)
				if err != nil {
					return nil, err
				}
				country := mcp.String(args, "country")
				if country == "" {
					if a, err := config.Resolve(c.account); err == nil {
						country = a.Country
					}
				}
				if country == "" {
					country = "ES"
				}
				return after.Warranty(orders, mcp.String(args, "query"), country, nil, nil, time.Now()), nil
			},
		},
		{
			Name:        "returns_window",
			Description: "Orders still inside their return window, soonest deadline first (from the stored orders).",
			Schema:      mcp.Schema(map[string]any{"account": accountProp, "days": mcp.Num("only windows closing within this many days")}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				if c.account == "" {
					c.account = "all"
				}
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				orders, err := allOrders(db, c)
				if err != nil {
					return nil, err
				}
				lines := after.Window(orders, time.Now())
				if lines == nil {
					lines = []after.WindowLine{}
				}
				if d := int(mcp.Number(args, "days")); d > 0 {
					var keep []after.WindowLine
					for _, l := range lines {
						if l.DaysLeft <= d {
							keep = append(keep, l)
						}
					}
					lines = keep
				}
				return lines, nil
			},
		},
		{
			Name:        "audit",
			Description: "Match returned or cancelled orders against the money that came back (payments page, gift card log, order-page refund totals) and give a verdict per order.",
			Schema:      mcp.Schema(map[string]any{"account": accountProp, "market": marketProp, "since": mcp.Str("only orders on or after YYYY-MM-DD")}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				accts, err := c.accounts()
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, a := range accts {
					sfs, err := c.storefronts(a)
					if err != nil {
						return nil, err
					}
					for _, sf := range sfs {
						in, err := auditInput(db, a, sf, mcp.String(args, "since"))
						if err != nil {
							return nil, err
						}
						if len(in.Orders) == 0 || len(in.Transactions) == 0 {
							continue
						}
						out = append(out, map[string]any{"account": a.Name, "market": sf.Code, "report": audit.Run(in)})
					}
				}
				return out, nil
			},
		},
		{
			Name:        "spend",
			Description: "What the orders cost, grouped by year, month, account, market or account/market, with what came back as refunds.",
			Schema:      mcp.Schema(map[string]any{"by": mcp.Str("year (default), month, account, market, account/market"), "since": mcp.Str("YYYY-MM-DD"), "until": mcp.Str("YYYY-MM-DD"), "account": accountProp, "market": marketProp}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				if c.account == "" {
					c.account = "all"
				}
				if c.market == "" {
					c.market = "all"
				}
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				q, err := c.filter()
				if err != nil {
					return nil, err
				}
				q.Since, q.Until = mcp.String(args, "since"), mcp.String(args, "until")
				orders, err := db.Orders(q)
				if err != nil {
					return nil, err
				}
				refunded, err := refundLedger(db, c)
				if err != nil {
					return nil, err
				}
				by := mcp.String(args, "by")
				if by == "" {
					by = "year"
				}
				return money.Spend(orders, by, refunded), nil
			},
		},
		{
			Name:        "health",
			Description: "Return rate per account and storefront over rolling 3, 6 and 12 months, with trend and a warning when it sits where Amazon restricts accounts.",
			Schema:      mcp.Schema(map[string]any{"account": accountProp, "market": marketProp}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				c := mcpCommon(args)
				if c.account == "" {
					c.account = "all"
				}
				if c.market == "" {
					c.market = "all"
				}
				db, err := openStore()
				if err != nil {
					return nil, err
				}
				defer db.Close()
				accts, err := c.accounts()
				if err != nil {
					return nil, err
				}
				refunded, err := refundLedger(db, c)
				if err != nil {
					return nil, err
				}
				out := []health.Report{}
				for _, a := range accts {
					sfs, err := c.storefronts(a)
					if err != nil {
						return nil, err
					}
					for _, sf := range sfs {
						orders, err := db.Orders(store.Query{Accounts: []string{a.Name}, Markets: []string{sf.Code}})
						if err != nil {
							return nil, err
						}
						if len(orders) > 0 {
							out = append(out, health.Run(a.Name, sf.Code, orders, refunded, time.Now()))
						}
					}
				}
				return out, nil
			},
		},
		{
			Name:        "markets",
			Description: "The storefronts amz knows: code, host, country, currency, whether ASINs are shared with the EU catalogue, Camel coverage, legal guarantee years.",
			Schema:      mcp.Schema(map[string]any{}),
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				return market.All(), nil
			},
		},
	}
}
