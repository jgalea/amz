package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/pull"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("orders", "list orders from the store (--live to read the site)", ordersCmd)
	register("invoices", "save each order's printable summary and invoice PDFs", invoicesCmd)
	register("refunds", "refunds from the payments page and the returns centre", refundsCmd)
	register("giftcard", "gift card balance activity (refunds land here too)", giftcardCmd)
	register("dump", "orders, invoices, refunds and gift card activity into a directory", dumpCmd)
	register("page", "save a rendered page (orders|transactions|returns|giftcard), for fixing selectors", pageCmd)
}

func ordersCmd(args []string) error {
	c := flags("orders", "[--year N | --all] [--since D] [--grep TEXT] [--asin X] [--live]")
	var year int
	var all, live bool
	var since, until, grep, asin string
	c.fs.IntVar(&year, "year", 0, "only orders placed in that year")
	c.fs.BoolVar(&all, "all", false, "every year (live: walk them all)")
	c.fs.StringVar(&since, "since", "", "only orders on or after YYYY-MM-DD")
	c.fs.StringVar(&until, "until", "", "only orders on or before YYYY-MM-DD")
	c.fs.StringVar(&grep, "grep", "", "only orders with an item title matching this")
	c.fs.StringVar(&asin, "asin", "", "only orders holding this ASIN")
	c.fs.BoolVar(&live, "live", false, "read the site instead of the store (and update the store)")
	if err := c.parse(args); err != nil {
		return err
	}
	if year != 0 && all {
		return fmt.Errorf("--year and --all do not go together")
	}
	if _, err := isoDate(since); err != nil {
		return err
	}
	if _, err := isoDate(until); err != nil {
		return err
	}
	if year != 0 {
		since, until = fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year)
	}

	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	if live {
		acct, err := c.one()
		if err != nil {
			return err
		}
		sfs, err := c.storefronts(acct)
		if err != nil {
			return err
		}
		for _, sf := range sfs {
			res := pull.Run(db, acct, sf, pull.Options{Full: all, Since: since, Only: []string{"orders"}, Headless: !c.show, Log: logLine(os.Stderr)})
			if res.SignedOut {
				return amazon.ErrSignedOut
			}
			if res.Error != "" {
				return fmt.Errorf("%s/%s: %s", res.Account, res.Market, res.Error)
			}
		}
		if year == 0 && !all && since == "" {
			since = fmt.Sprintf("%d-01-01", time.Now().Year())
		}
	}

	q, err := c.filter()
	if err != nil {
		return err
	}
	q.Since, q.Until, q.Grep, q.ASIN = since, until, grep, asin
	got, err := db.Orders(q)
	if err != nil {
		return err
	}
	switch {
	case c.asJSON:
		return emitJSON(got)
	case c.asCSV:
		return writeOrdersCSV(os.Stdout, got)
	}
	if len(got) == 0 {
		fmt.Fprintln(os.Stderr, "no orders in the store for that filter (run `amz sync` first, or add --live)")
		return nil
	}
	printOrders(got, c.account == "all" || c.market == "all")
	return nil
}

func printOrders(got []store.Order, labelled bool) {
	for _, o := range got {
		header := o.ID
		if labelled {
			header = o.Account + "/" + o.Market + "  " + header
		}
		if o.Date != "" {
			header += "  " + o.Date
		}
		if o.Total != "" {
			header += "  " + o.Total
		}
		if o.Status != "" {
			header += "  [" + o.Status + "]"
		}
		fmt.Println(header)
		for _, it := range o.Items {
			line := "    " + truncate(it.Title, 96)
			if it.Qty != "" && it.Qty != "1" {
				line += "  x" + it.Qty
			}
			if it.ASIN != "" {
				line += "  " + it.ASIN
			}
			fmt.Println(line)
		}
		for _, r := range o.Returns {
			if len(r) < 160 {
				fmt.Println("    ! " + r)
			}
		}
		if o.ReturnUntil != "" {
			fmt.Println("    returnable until " + o.ReturnUntil)
		}
		fmt.Println("    " + o.URL)
	}
}

func writeOrdersCSV(w io.Writer, got []store.Order) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"account", "market", "order_id", "date", "date_iso", "total", "currency", "status", "asin", "title", "qty", "returns", "return_until", "url"})
	for _, o := range got {
		returns := strings.Join(o.Returns, " | ")
		if len(o.Items) == 0 {
			cw.Write([]string{o.Account, o.Market, o.ID, o.Date, o.DateISO, o.Total, o.Currency, o.Status, "", "", "", returns, o.ReturnUntil, o.URL})
			continue
		}
		for _, it := range o.Items {
			cw.Write([]string{o.Account, o.Market, o.ID, o.Date, o.DateISO, o.Total, o.Currency, o.Status, it.ASIN, it.Title, it.Qty, returns, o.ReturnUntil, o.URL})
		}
	}
	cw.Flush()
	return cw.Error()
}

// fetchYears walks the site for the requested years: one year, all of
// them, or (neither) the current one.
func fetchYears(ctx context.Context, site amazon.Site, year int, all bool) ([]amazon.Order, error) {
	var years []int
	switch {
	case all:
		ys, err := amazon.Years(ctx, site)
		if err != nil {
			return nil, err
		}
		years = ys
	case year != 0:
		years = []int{year}
	default:
		years = []int{time.Now().Year()}
	}
	var out []amazon.Order
	for _, y := range years {
		got, err := amazon.ListYear(ctx, site, y, func(page, total int) {
			fmt.Fprintf(os.Stderr, "\r%d: page %d, %d orders", y, page, total)
		})
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return out, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func invoicesCmd(args []string) error {
	if len(args) > 0 && args[0] == "export" {
		return invoicesExport(args[1:])
	}
	c := flags("invoices", "--out DIR [--year N | --all]  |  invoices export --quarter 2026Q3 --to DIR")
	var out string
	var year int
	var all bool
	c.fs.StringVar(&out, "out", "", "directory to write into")
	c.fs.IntVar(&year, "year", 0, "only orders placed in that year")
	c.fs.BoolVar(&all, "all", false, "every year the account has")
	if err := c.parse(args); err != nil {
		return err
	}
	if out == "" {
		return fmt.Errorf("invoices needs --out DIR")
	}
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	s, err := pull.Session(acct, !c.show, 3*time.Hour)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, sf := range sfs {
		site := amazon.Site{TLD: sf.Code}
		got, err := fetchYears(s.Ctx, site, year, all)
		if err != nil {
			return err
		}
		dir := out
		if len(sfs) > 1 {
			dir = filepath.Join(out, sf.Code)
		}
		fmt.Fprintf(os.Stderr, "%s: %d orders; saving to %s\n", sf.Code, len(got), dir)
		if err := amazon.SaveInvoices(s.Ctx, site, got, dir, logLine(os.Stderr)); err != nil {
			return err
		}
	}
	return nil
}

func refundsCmd(args []string) error {
	c := flags("refunds", "[--all] [--live]")
	var all, live bool
	c.fs.BoolVar(&all, "all", false, "every transaction, not just refunds")
	c.fs.BoolVar(&live, "live", false, "read the site first (and update the store)")
	if err := c.parse(args); err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	if live {
		if err := liveSync(db, c, []string{"transactions", "returns"}); err != nil {
			return err
		}
	}
	q, err := c.filter()
	if err != nil {
		return err
	}
	tx, err := db.Transactions(q)
	if err != nil {
		return err
	}
	if !all {
		var only []store.Transaction
		for _, t := range tx {
			if t.Refund {
				only = append(only, t)
			}
		}
		tx = only
	}
	rets, err := db.Returns(q)
	if err != nil {
		return err
	}
	rep := map[string]any{"transactions": tx, "returns": rets}
	if c.asJSON {
		return emitJSON(rep)
	}
	if c.asCSV {
		w := csv.NewWriter(os.Stdout)
		w.Write([]string{"account", "market", "date", "date_iso", "amount", "method", "order_ids", "merchant", "status", "refund"})
		for _, t := range tx {
			w.Write([]string{t.Account, t.Market, t.Date, t.DateISO, t.Amount, t.Method, strings.Join(t.OrderIDs, " "), t.Merchant, t.Status, fmt.Sprint(t.Refund)})
		}
		w.Flush()
		return w.Error()
	}
	fmt.Printf("Transactions (%d)\n", len(tx))
	for _, t := range tx {
		fmt.Printf("  %-12s %12s  %-20s %-20s %s\n", t.DateISO, t.Amount, truncate(t.Method, 20), strings.Join(t.OrderIDs, ","), truncate(t.Merchant, 40))
	}
	fmt.Printf("Returns (%d)\n", len(rets))
	for _, r := range rets {
		fmt.Printf("  %s  %s\n", r.OrderID, truncate(r.Title, 70))
		if r.Status != "" {
			fmt.Println("      " + r.Status)
		}
	}
	return nil
}

// liveSync runs a partial sync for the selected account and storefronts.
func liveSync(db *store.Store, c *common, only []string) error {
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	for _, sf := range sfs {
		res := pull.Run(db, acct, sf, pull.Options{Only: only, Headless: !c.show, Log: logLine(os.Stderr)})
		if res.SignedOut {
			return amazon.ErrSignedOut
		}
		if res.Error != "" {
			return fmt.Errorf("%s/%s: %s", res.Account, res.Market, res.Error)
		}
	}
	return nil
}

func giftcardCmd(args []string) error {
	c := flags("giftcard", "[--live]")
	var live bool
	c.fs.BoolVar(&live, "live", false, "read the site first (and update the store)")
	if err := c.parse(args); err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	if live {
		if err := liveSync(db, c, []string{"giftcard"}); err != nil {
			return err
		}
	}
	q, err := c.filter()
	if err != nil {
		return err
	}
	got, err := db.GiftCard(q)
	if err != nil {
		return err
	}
	if c.asJSON {
		return emitJSON(got)
	}
	if len(got) == 0 {
		fmt.Fprintln(os.Stderr, "no gift card activity in the store")
		return nil
	}
	for _, g := range got {
		fmt.Printf("%-12s %12s  %-20s %s\n", g.DateISO, g.Amount, g.OrderID, truncate(g.Description, 60))
	}
	return nil
}

func dumpCmd(args []string) error {
	c := flags("dump", "--out DIR [--year N]")
	var out string
	var year int
	c.fs.StringVar(&out, "out", "", "directory to write into")
	c.fs.IntVar(&year, "year", 0, "only that year (default: every year)")
	if err := c.parse(args); err != nil {
		return err
	}
	if out == "" {
		return fmt.Errorf("dump needs --out DIR")
	}
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	s, err := pull.Session(acct, !c.show, 4*time.Hour)
	if err != nil {
		return err
	}
	defer s.Close()
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	for _, sf := range sfs {
		dir := out
		if len(sfs) > 1 {
			dir = filepath.Join(out, sf.Code)
		}
		if err := private.Dir(dir); err != nil {
			return err
		}
		site := amazon.Site{TLD: sf.Code}
		got, err := fetchYears(s.Ctx, site, year, year == 0)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(dir, "orders.json"), got); err != nil {
			return err
		}
		f, err := private.Create(filepath.Join(dir, "orders.csv"))
		if err != nil {
			return err
		}
		var rows []store.Order
		for _, o := range got {
			rows = append(rows, store.Order{Account: acct.Name, Market: sf.Code, Order: o, Currency: store.Currency(o.Total, sf.Currency)})
		}
		if err := writeOrdersCSV(f, rows); err != nil {
			f.Close()
			return err
		}
		f.Close()
		fmt.Fprintf(os.Stderr, "%s: %d orders written\n", sf.Code, len(got))
		if err := db.UpsertOrders(acct.Name, sf.Code, sf.Currency, got); err != nil {
			return err
		}

		if err := amazon.SaveInvoices(s.Ctx, site, got, filepath.Join(dir, "invoices"), logLine(os.Stderr)); err != nil {
			return err
		}
		tx, err := amazon.LoadTransactions(s.Ctx, site, func(n int) { fmt.Fprintf(os.Stderr, "\rtransactions: %d", n) })
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		rets, _, err := amazon.LoadReturns(s.Ctx, site)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(dir, "refunds.json"), map[string]any{"transactions": tx, "returns": rets}); err != nil {
			return err
		}
		gc, _, err := amazon.LoadGiftCard(s.Ctx, site)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(dir, "giftcard.json"), gc); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s: %d transactions, %d returns, %d gift card entries written\n", sf.Code, len(tx), len(rets), len(gc))
		d, err := pull.ReadDump(dir)
		if err != nil {
			return err
		}
		if _, err := pull.Import(db, acct.Name, sf, d); err != nil {
			return err
		}
	}
	return nil
}

func pageCmd(args []string) error {
	c := flags("page", "orders|transactions|returns|giftcard [FILE]")
	var year int
	c.fs.IntVar(&year, "year", 0, "orders page for that year")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) == 0 {
		return fmt.Errorf("page needs one of: orders, transactions, returns, giftcard")
	}
	which := c.args[0]
	out := which + ".html"
	if len(c.args) > 1 {
		out = c.args[1]
	}
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	site := amazon.Site{TLD: sfs[0].Code}
	var u, ready string
	switch which {
	case "orders":
		u, ready = site.OrdersURL(year, 0), ".order-card, .js-order-card, #ordersContainer"
	case "transactions":
		u, ready = site.TransactionsURL(), `.apx-transactions-line-item-component-container, [data-testid="method-details-number"]`
	case "returns":
		u, ready = site.ReturnsURL(), "main, #a-page"
	case "giftcard":
		u, ready = site.GiftCardURL(), "table, [data-testid], #a-page"
	default:
		return fmt.Errorf("unknown page %q (orders, transactions, returns, giftcard)", which)
	}
	s, err := pull.Session(acct, !c.show, 2*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()
	p, err := amazon.Load(s.Ctx, u, 25*time.Second, ready)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, []byte(p.HTML), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes) from %s\n", out, len(p.HTML), p.URL)
	return nil
}
