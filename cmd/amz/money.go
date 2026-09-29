package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jgalea/amz/internal/audit"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/money"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("spend", "what the orders cost, by year, month, account or market", spendCmd)
	register("bank", "bank match FILE: reconcile a Revolut, N26 or Wise CSV against Amazon's charges and refunds", bankCmd)
}

// refundLedger runs the audit for every selected account/storefront
// and returns what came back per order id.
func refundLedger(db *store.Store, c *common) (map[string]float64, error) {
	accts, err := c.accounts()
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, a := range accts {
		sfs, err := c.storefronts(a)
		if err != nil {
			return nil, err
		}
		for _, sf := range sfs {
			in, err := auditInput(db, a, sf, "")
			if err != nil {
				return nil, err
			}
			if len(in.Orders) == 0 {
				continue
			}
			rep := audit.Run(in)
			for _, group := range [][]audit.Line{rep.Returned, rep.Unmarked} {
				for _, l := range group {
					out[l.OrderID] = l.Refunded
				}
			}
		}
	}
	return out, nil
}

func spendCmd(args []string) error {
	c := flags("spend", "[--by year|month|account|market] [--since D] [--until D] [--account all]")
	var by, since, until string
	c.fs.StringVar(&by, "by", "year", "year, month, account, market, account/market")
	c.fs.StringVar(&since, "since", "", "only orders on or after YYYY-MM-DD")
	c.fs.StringVar(&until, "until", "", "only orders on or before YYYY-MM-DD")
	if err := c.parse(args); err != nil {
		return err
	}
	if _, err := isoDate(since); err != nil {
		return err
	}
	if _, err := isoDate(until); err != nil {
		return err
	}
	if c.account == "" {
		c.account = "all"
	}
	if c.market == "" {
		c.market = "all"
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	q, err := c.filter()
	if err != nil {
		return err
	}
	q.Since, q.Until = since, until
	orders, err := db.Orders(q)
	if err != nil {
		return err
	}
	refunded, err := refundLedger(db, c)
	if err != nil {
		return err
	}
	buckets := money.Spend(orders, by, refunded)
	switch {
	case c.asJSON:
		return emitJSON(buckets)
	case c.asCSV:
		w := csv.NewWriter(os.Stdout)
		w.Write([]string{by, "orders", "items", "spent", "refunded", "net", "currency"})
		for _, b := range buckets {
			w.Write([]string{b.Key, fmt.Sprint(b.Orders), fmt.Sprint(b.Items), fmt.Sprintf("%.2f", b.Spent), fmt.Sprintf("%.2f", b.Refunded), fmt.Sprintf("%.2f", b.Net), b.Currency})
		}
		w.Flush()
		return w.Error()
	}
	if len(buckets) == 0 {
		fmt.Println("no orders in the store for that selection")
		return nil
	}
	fmt.Printf("%-16s %6s %6s %12s %12s %12s  %s\n", strings.ToUpper(by), "ORDERS", "ITEMS", "SPENT", "REFUNDED", "NET", "CUR")
	var tot money.Bucket
	for _, b := range buckets {
		fmt.Printf("%-16s %6d %6d %12.2f %12.2f %12.2f  %s\n", b.Key, b.Orders, b.Items, b.Spent, b.Refunded, b.Net, b.Currency)
		tot.Orders += b.Orders
		tot.Items += b.Items
		tot.Spent += b.Spent
		tot.Refunded += b.Refunded
		tot.Net += b.Net
	}
	fmt.Printf("%-16s %6d %6d %12.2f %12.2f %12.2f\n", "total", tot.Orders, tot.Items, tot.Spent, tot.Refunded, tot.Net)
	fmt.Println("Spent is the order total on the card (cancelled orders count as their card total, usually zero); refunded is what the audit ledger matched.")
	return nil
}

func invoicesExport(args []string) error {
	c := flags("invoices export", "--quarter 2026Q3 [--account work] [--from DIR,DIR] [--to DIR] [--with-summary]")
	var quarter, from, to string
	var withSummary bool
	c.fs.StringVar(&quarter, "quarter", "", "quarter to export, like 2026Q3")
	c.fs.StringVar(&from, "from", "", "comma list of directories holding invoice PDFs (dump output); default ~/.amz/invoices")
	c.fs.StringVar(&to, "to", "", "accountant folder root (default: invoices.dir in config.json)")
	c.fs.BoolVar(&withSummary, "with-summary", false, "copy the printable summary for orders with no invoice, marked NOT-AN-INVOICE")
	if err := c.parse(args); err != nil {
		return err
	}
	q, err := money.ParseQuarter(quarter)
	if err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	if to == "" {
		to = settings.Invoices.Dir
	}
	if to == "" {
		return fmt.Errorf("invoices export needs --to DIR (or invoices.dir in %s)", filepath.Join(config.Dir(), "config.json"))
	}
	var srcs []string
	if from == "" {
		srcs = []string{filepath.Join(config.Dir(), "invoices")}
	} else {
		for _, d := range strings.Split(from, ",") {
			if d = strings.TrimSpace(d); d != "" {
				srcs = append(srcs, d)
			}
		}
	}
	accts, err := c.accounts()
	if err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	start, end := q.Range()
	type report struct {
		Account string           `json:"account"`
		Dest    string           `json:"dest"`
		Orders  []money.Exported `json:"orders"`
		Missing int              `json:"missing"`
	}
	var reports []report
	for _, a := range accts {
		orders, err := db.Orders(store.Query{Accounts: []string{a.Name}, Since: start, Until: end})
		if err != nil {
			return err
		}
		got, dest, err := money.Export(orders, q, srcs, to, a.Name, withSummary)
		if err != nil {
			return err
		}
		r := report{Account: a.Name, Dest: dest, Orders: got}
		for _, e := range got {
			if e.Missing {
				r.Missing++
			}
		}
		reports = append(reports, r)
	}
	if c.asJSON {
		return emitJSON(reports)
	}
	for _, r := range reports {
		fmt.Printf("%s: %d orders in %s, %d without an invoice PDF; files under %s\n", r.Account, len(r.Orders), q, r.Missing, r.Dest)
		for _, e := range r.Orders {
			if e.Missing {
				fmt.Printf("  missing  %s  %s  %s  %s\n", e.Date, e.OrderID, e.Total, truncate(e.Title, 50))
			}
		}
	}
	fmt.Println("Orders marked missing have no downloadable invoice in the source directories: run `amz invoices --out DIR` for the period first, or request one from the seller on the order page.")
	return nil
}

func bankCmd(args []string) error {
	c := flags("bank", "match FILE [--window 4] [--account all]")
	var window int
	c.fs.IntVar(&window, "window", 4, "days a bank line may sit from the Amazon transaction")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) < 2 || c.args[0] != "match" {
		return fmt.Errorf("usage: amz bank match FILE.csv")
	}
	f, err := os.Open(c.args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	rows, source, err := money.Statement(f)
	if err != nil {
		return err
	}
	if c.account == "" {
		c.account = "all"
	}
	if c.market == "" {
		c.market = "all"
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	q, err := c.filter()
	if err != nil {
		return err
	}
	tx, err := db.Transactions(q)
	if err != nil {
		return err
	}
	matches, unmatched := money.Reconcile(rows, tx, window)
	if c.asJSON {
		return emitJSON(map[string]any{"source": source, "bank_rows": len(rows), "matches": matches, "amazon_unmatched": unmatched})
	}
	fmt.Printf("%s statement: %d Amazon lines; store: %d Amazon transactions\n", source, len(rows), len(tx))
	if len(rows) == 0 {
		return fmt.Errorf("no Amazon lines found in the statement; nothing was compared")
	}
	problems := 0
	fmt.Printf("%-10s %10s  %-28s %s\n", "DATE", "AMOUNT", "STATEMENT", "RESULT")
	for _, m := range matches {
		res := m.State
		if m.Tx != nil {
			res = fmt.Sprintf("matched %s %s", m.Tx.DateISO, strings.Join(m.Tx.OrderIDs, ","))
			if m.Days > 0 {
				res += fmt.Sprintf(" (%d days apart)", m.Days)
			}
		} else {
			problems++
		}
		fmt.Printf("%-10s %10.2f  %-28s %s\n", m.Bank.Date, m.Bank.Amount, truncate(m.Bank.Description, 28), res)
	}
	if len(unmatched) > 0 {
		fmt.Printf("\nAmazon transactions in the statement period with no bank line (%d):\n", len(unmatched))
		for _, t := range unmatched {
			fmt.Printf("  %s %10s  %-20s %s\n", t.DateISO, t.Amount, truncate(t.Method, 20), strings.Join(t.OrderIDs, ","))
		}
		problems += len(unmatched)
	}
	fmt.Printf("\n%d bank lines matched, %d to look at.\n", len(matches)-problems+len(unmatched), problems)
	return nil
}
