package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jgalea/amz/internal/audit"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/pull"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("sync", "read orders, returns, transactions and gift card activity into the store", syncCmd)
	register("import", "load a dump directory (orders.json, refunds.json, ...) into the store", importCmd)
	register("audit", "match returned orders against money that came back", auditCmd)
}

func syncCmd(args []string) error {
	c := flags("sync", "[--account NAME|all] [--market CODE|all] [--full] [--since D] [--only orders,transactions] [--summaries]")
	var full, summaries bool
	var since, only string
	c.fs.BoolVar(&full, "full", false, "walk every year and every transaction, not just what changed since the last sync")
	c.fs.StringVar(&since, "since", "", "re-read from this date (YYYY-MM-DD) instead of the last sync")
	c.fs.StringVar(&only, "only", "", "comma list of: orders, returns, transactions, giftcard, summaries")
	c.fs.BoolVar(&summaries, "summaries", false, "also fetch the order page for returned orders without one (one load per order)")
	if err := c.parse(args); err != nil {
		return err
	}
	if _, err := isoDate(since); err != nil {
		return err
	}
	var onlyList []string
	if only != "" {
		for _, w := range strings.Split(only, ",") {
			w = strings.TrimSpace(w)
			switch w {
			case "orders", "returns", "transactions", "giftcard", "summaries":
				onlyList = append(onlyList, w)
				if w == "summaries" {
					summaries = true
				}
			default:
				return fmt.Errorf("--only: unknown item %q", w)
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

	var results []pull.Result
	for _, a := range accts {
		sfs, err := c.storefronts(a)
		if err != nil {
			return err
		}
		for _, sf := range sfs {
			res := pull.Run(db, a, sf, pull.Options{Full: full, Since: since, Only: onlyList, Summaries: summaries, Headless: !c.show, Log: logLine(os.Stderr)})
			results = append(results, res)
			if res.SignedOut {
				fmt.Fprintf(os.Stderr, "%s/%s: signed out; run `amz login --account %s --market %s`\n", a.Name, sf.Code, a.Name, sf.Code)
				break
			}
			if res.Error != "" {
				fmt.Fprintf(os.Stderr, "%s/%s: %s\n", a.Name, sf.Code, res.Error)
			}
		}
	}
	if c.asJSON {
		return emitJSON(results)
	}
	failed := false
	for _, r := range results {
		state := fmt.Sprintf("%d orders, %d new transactions, %d new gift card rows, %d returns", r.Orders, r.Transactions, r.GiftCard, r.Returns)
		if r.Summaries > 0 {
			state += fmt.Sprintf(", %d summaries", r.Summaries)
		}
		if r.SignedOut {
			state, failed = "signed out", true
		} else if r.Error != "" {
			state, failed = "error: "+r.Error, true
		}
		fmt.Printf("%s/%s: %s\n", r.Account, r.Market, state)
	}
	if failed {
		return fmt.Errorf("some syncs did not finish")
	}
	return nil
}

func importCmd(args []string) error {
	c := flags("import", "DIR --account NAME [--market es]")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return fmt.Errorf("import needs the dump directory")
	}
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	if len(sfs) != 1 {
		return fmt.Errorf("import needs one --market (a dump is from one storefront)")
	}
	d, err := pull.ReadDump(c.args[0])
	if err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	res, err := pull.Import(db, acct.Name, sfs[0], d)
	if err != nil {
		return err
	}
	if !acct.Exists() {
		if err := acct.Save(); err != nil {
			return err
		}
	}
	if c.asJSON {
		return emitJSON(res)
	}
	fmt.Printf("%s/%s: %d orders, %d new transactions, %d returns, %d new gift card rows, %d summaries imported\n", res.Account, res.Market, res.Orders, res.Transactions, res.Returns, res.GiftCard, res.Summaries)
	return nil
}

func auditCmd(args []string) error {
	c := flags("audit", "[--account NAME|all] [--market CODE|all] [--since D] [--from DIR]")
	var since, from string
	c.fs.StringVar(&since, "since", "", "only orders placed on or after YYYY-MM-DD")
	c.fs.StringVar(&from, "from", "", "audit a dump directory instead of the store")
	if err := c.parse(args); err != nil {
		return err
	}
	if _, err := isoDate(since); err != nil {
		return err
	}
	type labelled struct {
		Account string       `json:"account"`
		Market  string       `json:"market"`
		Report  audit.Report `json:"report"`
		Orders  int          `json:"orders"`
		Tx      int          `json:"transactions"`
		Gift    int          `json:"giftcard"`
	}
	var reports []labelled

	if from != "" {
		d, err := pull.ReadDump(from)
		if err != nil {
			return err
		}
		if len(d.Orders) == 0 || len(d.Transactions) == 0 {
			return fmt.Errorf("nothing to audit in %s: %d orders, %d transactions", from, len(d.Orders), len(d.Transactions))
		}
		in := audit.Input{Orders: d.Orders, Transactions: d.Transactions, GiftCard: d.GiftCard, Summaries: d.Summaries, Since: since}
		reports = append(reports, labelled{"dump", "", audit.Run(in), len(d.Orders), len(d.Transactions), len(d.GiftCard)})
	} else {
		db, err := openStore()
		if err != nil {
			return err
		}
		defer db.Close()
		accts, err := c.accounts()
		if err != nil {
			return err
		}
		for _, a := range accts {
			sfs, err := c.storefronts(a)
			if err != nil {
				return err
			}
			for _, sf := range sfs {
				in, err := auditInput(db, a, sf, since)
				if err != nil {
					return err
				}
				if len(in.Orders) == 0 {
					continue
				}
				if len(in.Transactions) == 0 {
					fmt.Fprintf(os.Stderr, "%s/%s: %d orders but no transactions in the store; skipped (sync transactions first)\n", a.Name, sf.Code, len(in.Orders))
					continue
				}
				reports = append(reports, labelled{a.Name, sf.Code, audit.Run(in), len(in.Orders), len(in.Transactions), len(in.GiftCard)})
			}
		}
		if len(reports) == 0 {
			return fmt.Errorf("nothing to audit: the store has no orders for that selection")
		}
	}
	switch {
	case c.asJSON:
		return emitJSON(reports)
	case c.asCSV:
		cw := csv.NewWriter(os.Stdout)
		cw.Write([]string{"account", "market", "order_id", "date", "title", "total", "charged", "refunded", "gap", "verdict", "estimated", "sources", "marker"})
		for _, r := range reports {
			for _, group := range [][]audit.Line{r.Report.Returned, r.Report.Unmarked} {
				for _, l := range group {
					cw.Write([]string{r.Account, r.Market, l.OrderID, l.Date, l.Title, fmt.Sprintf("%.2f", l.TotalN), fmt.Sprintf("%.2f", l.Charged), fmt.Sprintf("%.2f", l.Refunded), fmt.Sprintf("%.2f", l.Gap), l.Verdict, fmt.Sprint(l.Estimated), strings.Join(l.Sources, "+"), l.Marker})
				}
			}
		}
		cw.Flush()
		return cw.Error()
	}
	for i, r := range reports {
		if i > 0 {
			fmt.Println()
		}
		if r.Market != "" {
			fmt.Printf("== %s / amazon.%s ==\n", r.Account, r.Market)
		}
		printAudit(os.Stdout, r.Report, r.Orders, r.Tx, r.Gift)
	}
	return nil
}

// auditInput assembles the audit's input for one account/storefront
// from the store.
func auditInput(db *store.Store, a config.Account, sf market.Storefront, since string) (audit.Input, error) {
	var in audit.Input
	q := store.Query{Accounts: []string{a.Name}, Markets: []string{sf.Code}}
	orders, err := db.Orders(q)
	if err != nil {
		return in, err
	}
	for _, o := range orders {
		in.Orders = append(in.Orders, o.Order)
	}
	tx, err := db.Transactions(q)
	if err != nil {
		return in, err
	}
	for _, t := range tx {
		in.Transactions = append(in.Transactions, t.Transaction)
	}
	gc, err := db.GiftCard(q)
	if err != nil {
		return in, err
	}
	for _, g := range gc {
		in.GiftCard = append(in.GiftCard, g.GiftCardEntry)
	}
	if in.Summaries, err = db.Summaries(a.Name, sf.Code); err != nil {
		return in, err
	}
	in.Since = since
	return in, nil
}

func printAudit(w io.Writer, res audit.Report, orders, transactions, gift int) {
	fmt.Fprintf(w, "Coverage: transactions from %s to %s (%d orders, %d transactions, %d gift card entries)\n", res.Oldest, res.Newest, orders, transactions, gift)
	if res.Excluded > 0 {
		fmt.Fprintf(w, "Excluded %d orders placed before %s: their refunds cannot be in this data.\n", res.Excluded, res.Oldest)
	}
	if res.Since != "" {
		fmt.Fprintf(w, "Only orders placed on or after %s.\n", res.Since)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Returned or cancelled orders (%d)\n", len(res.Returned))
	fmt.Fprintf(w, "  %-10s %-19s %-36s %9s %9s %9s %8s  %s\n", "DATE", "ORDER", "ITEM", "TOTAL", "CHARGED", "REFUNDED", "GAP", "VERDICT")
	for _, l := range res.Returned {
		printAuditLine(w, l)
	}
	if len(res.Unmarked) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Refunds on orders with no return wording (%d): settled, not missing\n", len(res.Unmarked))
		for _, l := range res.Unmarked {
			printAuditLine(w, l)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Verdicts:")
	for _, k := range []string{audit.Refunded, audit.Partial, audit.NoRefund, audit.NoTransactions, audit.NewerThanData, audit.Unmarked, audit.UnmarkedPart} {
		if n := res.Counts[k]; n > 0 {
			fmt.Fprintf(w, "  %-38s %d\n", k, n)
		}
	}
	fmt.Fprintln(w, "A ~ before an amount means a charge or refund covered several orders and was split evenly; treat it as an estimate.")
}

func printAuditLine(w io.Writer, l audit.Line) {
	est := " "
	if l.Estimated {
		est = "~"
	}
	fmt.Fprintf(w, "  %-10s %-19s %-36s %9.2f %s%8.2f %s%8.2f %8.2f  %s\n", l.Date, l.OrderID, truncate(l.Title, 36), l.TotalN, est, l.Charged, est, l.Refunded, l.Gap, l.Verdict)
}
