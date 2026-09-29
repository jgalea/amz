package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/after"
	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/audit"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/notify"
	"github.com/jgalea/amz/internal/pull"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("returns", "returns window: what is still returnable, and until when", returnsCmd)
	register("warranty", "find an item and where it stands under the legal guarantee", warrantyCmd)
	register("chase", "returns Amazon has had for N days with no refund; drafts a claim", chaseCmd)
	register("return", "return prepare ORDER [ASIN] --reason ...: fill the return form and stop before submit", returnCmd)
}

// allOrders reads every order the common flags select, across accounts.
func allOrders(db *store.Store, c *common) ([]store.Order, error) {
	q, err := c.filter()
	if err != nil {
		return nil, err
	}
	return db.Orders(q)
}

func returnsCmd(args []string) error {
	c := flags("returns", "[window] [--account all] [--days N] [--alert]")
	var days int
	var alert bool
	c.fs.IntVar(&days, "days", 0, "only windows closing within N days")
	c.fs.BoolVar(&alert, "alert", false, "send a notification when a window closes within 3 days (or --days)")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) > 0 && c.args[0] != "window" {
		return fmt.Errorf("returns: unknown subcommand %q (only window)", c.args[0])
	}
	if c.account == "" {
		c.account = "all"
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	orders, err := allOrders(db, c)
	if err != nil {
		return err
	}
	lines := after.Window(orders, time.Now())
	if days > 0 {
		var keep []after.WindowLine
		for _, l := range lines {
			if l.DaysLeft <= days {
				keep = append(keep, l)
			}
		}
		lines = keep
	}
	if alert {
		limit := days
		if limit == 0 {
			limit = 3
		}
		var soon []string
		for _, l := range lines {
			if l.DaysLeft <= limit {
				soon = append(soon, fmt.Sprintf("%s (%d days): %s", l.Until, l.DaysLeft, truncate(l.Title, 50)))
			}
		}
		if len(soon) > 0 {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			for _, r := range notify.Send(settings.Notify, fmt.Sprintf("amz: %d return window(s) closing", len(soon)), strings.Join(soon, "\n")) {
				if !r.OK {
					fmt.Fprintf(os.Stderr, "notify %s: %s\n", r.Channel, r.Error)
				}
			}
		}
	}
	switch {
	case c.asJSON:
		return emitJSON(lines)
	case c.asCSV:
		w := csv.NewWriter(os.Stdout)
		w.Write([]string{"account", "market", "order_id", "date", "title", "items", "total", "until", "days_left", "estimated", "url"})
		for _, l := range lines {
			w.Write([]string{l.Account, l.Market, l.OrderID, l.Date, l.Title, fmt.Sprint(l.Items), l.Total, l.Until, fmt.Sprint(l.DaysLeft), fmt.Sprint(l.Estimated), l.URL})
		}
		w.Flush()
		return w.Error()
	}
	if len(lines) == 0 {
		fmt.Println("nothing is inside a return window (from what the store holds; sync if it is stale)")
		return nil
	}
	fmt.Printf("%-10s %5s  %-8s %-19s %-46s %s\n", "UNTIL", "DAYS", "ACCOUNT", "ORDER", "ITEM", "TOTAL")
	for _, l := range lines {
		est := ""
		if l.Estimated {
			est = "~"
		}
		title := l.Title
		if l.Items > 1 {
			title = fmt.Sprintf("%s (+%d)", truncate(title, 40), l.Items-1)
		}
		fmt.Printf("%s%-9s %5d  %-8s %-19s %-46s %s\n", est, l.Until, l.DaysLeft, l.Account+"/"+l.Market, l.OrderID, truncate(title, 46), l.Total)
	}
	fmt.Println("~ means delivery (or order) date plus 30 days, since the card showed no deadline.")
	return nil
}

func warrantyCmd(args []string) error {
	c := flags("warranty", "QUERY [--account all] [--country PT]")
	var country string
	c.fs.StringVar(&country, "country", "", "buyer's country for the guarantee table (default: the account's)")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) == 0 {
		return fmt.Errorf("warranty needs a query: words from the title, an ASIN or an order id")
	}
	query := strings.Join(c.args, " ")
	if c.account == "" {
		c.account = "all"
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	orders, err := allOrders(db, c)
	if err != nil {
		return err
	}
	q, _ := c.filter()
	tx, err := db.Transactions(q)
	if err != nil {
		return err
	}
	sellers := map[string]string{}
	for _, t := range tx {
		for _, id := range t.OrderIDs {
			if _, ok := sellers[id]; !ok && !t.Refund {
				sellers[id] = t.Merchant
			}
		}
	}
	countries := map[string]string{}
	accts, _ := config.Accounts()
	for _, a := range accts {
		countries[a.Name] = a.Country
	}
	var lines []after.WarrantyLine
	byAccount := map[string][]store.Order{}
	for _, o := range orders {
		byAccount[o.Account] = append(byAccount[o.Account], o)
	}
	for acct, list := range byAccount {
		cc := country
		if cc == "" {
			cc = countries[acct]
		}
		if cc == "" {
			cc = "ES"
		}
		summaries := map[string]bool{}
		for _, o := range list {
			if sums, err := db.Summaries(acct, o.Market); err == nil {
				for id := range sums {
					summaries[id] = true
				}
			}
			break
		}
		lines = append(lines, after.Warranty(list, query, cc, sellers, summaries, time.Now())...)
	}
	if c.asJSON {
		return emitJSON(lines)
	}
	if len(lines) == 0 {
		fmt.Printf("nothing in the store matches %q\n", query)
		return nil
	}
	for _, l := range lines {
		state := fmt.Sprintf("%d days left", l.DaysLeft)
		if l.Expired {
			state = fmt.Sprintf("expired %d days ago", -l.DaysLeft)
		}
		if l.Years == 0 {
			state = "no statutory guarantee table for " + l.Country
		}
		fmt.Printf("%s  %s  %s  %s\n", l.Date, l.OrderID, l.Total, truncate(l.Title, 70))
		fmt.Printf("    guarantee: %d years in %s, until %s (%s)\n", l.Years, l.Country, l.Until, state)
		if l.Seller != "" {
			fmt.Printf("    paid to: %s\n", l.Seller)
		}
		if l.Returned {
			fmt.Println("    note: this order carries return or refund wording")
		}
		fmt.Printf("    order page: %s\n", l.URL)
		if l.Invoice != "" {
			fmt.Printf("    invoice popover: %s\n", l.Invoice)
		}
		if l.Summary {
			fmt.Println("    order page summary is in the store")
		}
	}
	fmt.Println("The seller answers for conformity under the legal guarantee; for marketplace sellers that is the seller, not Amazon.")
	return nil
}

func chaseCmd(args []string) error {
	c := flags("chase", "[--account all] [--days 14] [--out DIR]")
	var days int
	var out string
	c.fs.IntVar(&days, "days", 14, "returns open at least this many days")
	c.fs.StringVar(&out, "out", filepath.Join(config.Dir(), "claims"), "where to write claim drafts")
	if err := c.parse(args); err != nil {
		return err
	}
	if c.account == "" {
		c.account = "all"
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	accts, err := c.accounts()
	if err != nil {
		return err
	}
	var lines []after.ChaseLine
	for _, a := range accts {
		sfs, err := c.storefronts(a)
		if err != nil {
			return err
		}
		for _, sf := range sfs {
			in, err := auditInput(db, a, sf, "")
			if err != nil {
				return err
			}
			refunded := map[string]float64{}
			rep := audit.Run(in)
			for _, group := range [][]audit.Line{rep.Returned, rep.Unmarked} {
				for _, l := range group {
					refunded[l.OrderID] = l.Refunded
				}
			}
			q := store.Query{Accounts: []string{a.Name}, Markets: []string{sf.Code}}
			orders, err := db.Orders(q)
			if err != nil {
				return err
			}
			rets, err := db.Returns(q)
			if err != nil {
				return err
			}
			lines = append(lines, after.Chase(orders, rets, refunded, in.Summaries, days, time.Now())...)
		}
	}
	if c.asJSON {
		return emitJSON(lines)
	}
	if len(lines) == 0 {
		fmt.Printf("no return older than %d days is waiting for a refund\n", days)
		return nil
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	for _, l := range lines {
		path := filepath.Join(out, l.OrderID+".txt")
		if err := os.WriteFile(path, []byte(after.Draft(l, time.Now())), 0o600); err != nil {
			return err
		}
		fmt.Printf("%s  %s  %s  %d days, refunded %.2f  %s\n    draft: %s\n", l.ReturnDate, l.OrderID, l.Total, l.DaysOpen, l.Refunded, truncate(l.Title, 50), path)
	}
	fmt.Println("Drafts are text to paste into Amazon's contact form; nothing was sent.")
	return nil
}

func returnCmd(args []string) error {
	c := flags("return", "prepare ORDER [ASIN] --reason TEXT [--comment TEXT] [--account NAME] [--market es]")
	var reason, comment string
	c.fs.StringVar(&reason, "reason", "", "words from the return reason to pick (e.g. 'defective', 'no longer needed')")
	c.fs.StringVar(&comment, "comment", "", "text for the comment box, if the flow has one")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) < 2 || c.args[0] != "prepare" {
		return fmt.Errorf("usage: amz return prepare ORDER [ASIN] --reason TEXT")
	}
	if reason == "" {
		return fmt.Errorf("return prepare needs --reason")
	}
	orderID := c.args[1]
	asin := ""
	if len(c.args) > 2 {
		asin = c.args[2]
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
	// Always visible: the person finishes (or abandons) the form.
	s, err := pull.Session(acct, false, 0)
	if err != nil {
		return err
	}
	defer s.Close()
	rp, err := amazon.PrepareReturn(s.Ctx, site, orderID, asin, reason, comment)
	if c.asJSON {
		if err != nil {
			rp.Stopped = "error: " + err.Error()
		}
		emitJSON(rp)
	} else {
		for _, step := range rp.Steps {
			fmt.Println("- " + step)
		}
		if err != nil {
			fmt.Println("- error: " + err.Error())
		}
		fmt.Println("Stopped before: " + rp.Stopped)
	}
	fmt.Fprintln(os.Stderr, "The window stays open. Review the form and press the button yourself, or close it. Press Enter here when done.")
	bufio.NewReader(os.Stdin).ReadString('\n')
	return err
}
