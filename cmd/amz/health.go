package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/health"
	"github.com/jgalea/amz/internal/notices"
	"github.com/jgalea/amz/internal/notify"
	"github.com/jgalea/amz/internal/store"
)

func init() {
	register("health", "return rate per account over 3, 6 and 12 months, with a warning when it trends toward restriction", healthCmd)
	register("notices", "scan a mailbox (IMAP) or a directory of .eml files for Amazon account notices", noticesCmd)
}

func healthCmd(args []string) error {
	c := flags("health", "[--account all] [--market all] [--alert]")
	var alert bool
	c.fs.BoolVar(&alert, "alert", false, "send a notification when an account is at warn or critical level")
	if err := c.parse(args); err != nil {
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
	accts, err := c.accounts()
	if err != nil {
		return err
	}
	refunded, err := refundLedger(db, c)
	if err != nil {
		return err
	}
	var reports []health.Report
	for _, a := range accts {
		sfs, err := c.storefronts(a)
		if err != nil {
			return err
		}
		for _, sf := range sfs {
			orders, err := db.Orders(store.Query{Accounts: []string{a.Name}, Markets: []string{sf.Code}})
			if err != nil {
				return err
			}
			if len(orders) == 0 {
				continue
			}
			reports = append(reports, health.Run(a.Name, sf.Code, orders, refunded, time.Now()))
		}
	}
	if alert {
		var lines []string
		for _, r := range reports {
			if r.Level != "ok" {
				lines = append(lines, fmt.Sprintf("%s/%s: %s, 3m rate %.0f%% (%s)", r.Account, r.Market, r.Level, r.Windows[0].RateByCount*100, r.Trend))
			}
		}
		if len(lines) > 0 {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			for _, res := range notify.Send(settings.Notify, "amz: account health", strings.Join(lines, "\n")) {
				if !res.OK {
					fmt.Fprintf(os.Stderr, "notify %s: %s\n", res.Channel, res.Error)
				}
			}
		}
	}
	if c.asJSON {
		return emitJSON(reports)
	}
	if len(reports) == 0 {
		fmt.Println("no orders in the store for that selection")
		return nil
	}
	for _, r := range reports {
		fmt.Printf("%s / amazon.%s: %s, trend %s\n", r.Account, r.Market, r.Level, r.Trend)
		fmt.Printf("  %-7s %7s %9s %10s %12s %8s %8s  %s\n", "WINDOW", "ORDERS", "RETURNED", "SPENT", "RETURNED €", "BY N", "BY €", "LEVEL")
		for _, w := range r.Windows {
			fmt.Printf("  %-7s %7d %9d %10.2f %12.2f %7.1f%% %7.1f%%  %s\n", fmt.Sprintf("%dm", w.Months), w.Orders, w.Returned, w.Spent, w.ReturnedValue, w.RateByCount*100, w.RateByValue*100, w.Level)
		}
		if r.Warning != "" {
			fmt.Println("  ! " + r.Warning)
		}
	}
	fmt.Printf("Levels: warn at %.0f%%, critical at %.0f%% (by count or by value). Amazon publishes no threshold; these are the levels people report being restricted at.\n", health.WarnRate*100, health.CriticalRate*100)
	return nil
}

func noticesCmd(args []string) error {
	c := flags("notices", "[--dir DIR] [--since D] [--account NAME]")
	var dir, since string
	c.fs.StringVar(&dir, "dir", "", "read .eml files from this directory instead of IMAP")
	c.fs.StringVar(&since, "since", "", "only mail since YYYY-MM-DD (default: 90 days ago)")
	if err := c.parse(args); err != nil {
		return err
	}
	if _, err := isoDate(since); err != nil {
		return err
	}
	from := time.Now().AddDate(0, 0, -90)
	if since != "" {
		from, _ = time.Parse("2006-01-02", since)
	}
	var found []notices.Notice
	var scanned int
	var err error
	if dir != "" {
		found, scanned, err = notices.FromDir(dir)
	} else {
		settings, serr := config.LoadSettings()
		if serr != nil {
			return serr
		}
		found, scanned, err = notices.FromIMAP(settings.Mail, from, logLine(os.Stderr))
	}
	if err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	account := ""
	if c.account != "" && c.account != "all" {
		account = c.account
	}
	fresh := 0
	for i := range found {
		found[i].Account = account
		isNew, err := db.AddNotice(found[i])
		if err != nil {
			return err
		}
		if isNew {
			fresh++
		}
	}
	all, err := db.Notices()
	if err != nil {
		return err
	}
	if c.asJSON {
		return emitJSON(map[string]any{"scanned": scanned, "found": found, "new": fresh, "all": all})
	}
	fmt.Printf("scanned %d messages, %d notices, %d new\n", scanned, len(found), fresh)
	for _, n := range all {
		fmt.Printf("  %s  %-16s %s  (%s)\n", n.Date, n.Kind, truncate(n.Subject, 70), n.Sender)
	}
	if len(all) == 0 {
		fmt.Println("no Amazon account notices on record")
	}
	return nil
}
