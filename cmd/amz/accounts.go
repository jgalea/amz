package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/browser"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/pull"
)

func init() {
	register("accounts", "list, add or remove accounts", accountsCmd)
	register("login", "sign in by hand, once, in a dedicated Chrome profile", loginCmd)
	register("status", "is the account still signed in, and what the store holds", statusCmd)
	register("markets", "the storefronts amz knows", marketsCmd)
}

func accountsCmd(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		c := flags("accounts list", "")
		if err := c.parse(args); err != nil {
			return err
		}
		return listAccounts(c)
	case "add":
		return addAccount(args)
	case "remove":
		c := flags("accounts remove", "NAME")
		if err := c.parse(args); err != nil {
			return err
		}
		if len(c.args) != 1 {
			return fmt.Errorf("accounts remove needs the account name")
		}
		if err := config.Remove(c.args[0]); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "removed account %s (its Chrome profile and cookie jar are gone; the store keeps its orders)\n", c.args[0])
		return nil
	}
	return fmt.Errorf("accounts: unknown subcommand %q (list, add, remove)", sub)
}

func addAccount(args []string) error {
	c := flags("accounts add", "NAME [--type personal|business] [--email LABEL] [--country CC] [--home es] [--storefronts es,de,fr]")
	var typ, email, country, home, stores string
	c.fs.StringVar(&typ, "type", config.Personal, "personal or business")
	c.fs.StringVar(&email, "email", "", "label for the login (never used to send anything)")
	c.fs.StringVar(&country, "country", "", "delivery country, two letters (default: the home storefront's)")
	c.fs.StringVar(&home, "home", "", "home storefront (default: --market, else es)")
	c.fs.StringVar(&stores, "storefronts", "", "other storefronts this login uses, comma separated")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return fmt.Errorf("accounts add needs a name")
	}
	a := config.Account{Name: c.args[0], Type: typ, Email: email, Country: strings.ToUpper(country)}
	if home == "" {
		home = c.market
	}
	if home == "" || home == "all" {
		home = "es"
	}
	sf, err := market.Get(home)
	if err != nil {
		return err
	}
	a.Home = sf.Code
	if a.Country == "" {
		a.Country = sf.CountryCode
	}
	if stores != "" {
		list, err := market.Resolve(stores)
		if err != nil {
			return err
		}
		for _, s := range list {
			if s.Code != a.Home {
				a.Storefronts = append(a.Storefronts, s.Code)
			}
		}
	}
	if config.Migrate(); a.Exists() {
		old, err := config.Load(a.Name)
		if err != nil {
			return err
		}
		a.CreatedAt = old.CreatedAt
		fmt.Fprintf(os.Stderr, "updating account %s\n", a.Name)
	} else {
		a.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if err := a.Save(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "account %s (%s, home %s, country %s) saved; run `amz login --account %s` to sign in\n", a.Name, a.Type, a.Home, a.Country, a.Name)
	return nil
}

func listAccounts(c *common) error {
	list, err := config.Accounts()
	if err != nil {
		return err
	}
	type row struct {
		config.Account
		SignedIn  bool   `json:"signed_in"`
		Cookies   int    `json:"cookies"`
		LastLogin string `json:"last_login,omitempty"`
		Expires   string `json:"session_expires,omitempty"`
	}
	var rows []row
	for _, a := range list {
		st, err := a.Jar()
		if err != nil {
			return err
		}
		r := row{Account: a, SignedIn: st.SignedIn, Cookies: st.Cookies}
		if st.Exists {
			r.LastLogin = st.SavedAt.Format("2006-01-02 15:04")
		}
		if !st.Expires.IsZero() {
			r.Expires = st.Expires.Format("2006-01-02")
		}
		rows = append(rows, r)
	}
	if c.asJSON {
		return emitJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no accounts yet (run `amz accounts add NAME --type personal --home es`)")
		return nil
	}
	fmt.Printf("%-14s %-9s %-4s %-14s %-11s %-17s %s\n", "ACCOUNT", "TYPE", "HOME", "STOREFRONTS", "SESSION", "LAST LOGIN", "EMAIL")
	for _, r := range rows {
		state := "signed out"
		if r.SignedIn {
			state = "signed in"
		} else if r.Cookies == 0 {
			state = "never"
		}
		fmt.Printf("%-14s %-9s %-4s %-14s %-11s %-17s %s\n", r.Name, r.Type, r.Home, strings.Join(r.Storefronts, ","), state, r.LastLogin, r.Email)
	}
	return nil
}

func loginCmd(args []string) error {
	c := flags("login", "[--account NAME] [--market es]")
	if err := c.parse(args); err != nil {
		return err
	}
	acct, err := c.one()
	if err != nil {
		return err
	}
	sfs, err := c.storefronts(acct)
	if err != nil {
		return err
	}
	sf := sfs[0]
	site := amazon.Site{TLD: sf.Code}
	s, err := pull.Session(acct, false, 0)
	if err != nil {
		return err
	}
	defer s.Close()

	fmt.Fprintf(os.Stderr, "A Chrome window is opening. Sign in to %s there (account %q).\n", site.Base(), acct.Name)
	fmt.Fprintln(os.Stderr, "No clock on this: it waits, and picks the session up on its own.")
	fmt.Fprintln(os.Stderr, "Press Enter here once you are in if it has not noticed yet.")

	done := make(chan struct{})
	go func() {
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err == nil {
			close(done)
		}
	}()
	if err := amazon.WaitForLogin(s.Ctx, site, done); err != nil {
		return err
	}
	n, err := browser.SaveCookies(s.Ctx, acct.CookiePath())
	if err != nil {
		return fmt.Errorf("signed in, but saving the session failed: %w", err)
	}
	if !acct.Exists() || acct.Home == "" {
		acct.Home = sf.Code
	}
	if !acct.HasStorefront(sf.Code) {
		acct.Storefronts = append(acct.Storefronts, sf.Code)
	}
	if acct.CreatedAt == "" {
		acct.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if err := acct.Save(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s. Saved %d cookies to %s.\n", site.Base(), n, acct.CookiePath())
	return nil
}

func statusCmd(args []string) error {
	c := flags("status", "[--account NAME] [--market es] [--live]")
	var live bool
	c.fs.BoolVar(&live, "live", false, "open the browser and check the session against the site")
	if err := c.parse(args); err != nil {
		return err
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
	out := map[string]any{"account": acct.Name, "type": acct.Type, "market": site.TLD}

	jar, err := acct.Jar()
	if err != nil {
		return err
	}
	out["jar_signed_in"] = jar.SignedIn
	if live {
		s, err := pull.Session(acct, !c.show, 90*time.Second)
		if err != nil {
			return err
		}
		defer s.Close()
		ok, err := amazon.LoggedIn(s.Ctx, site)
		if err != nil {
			return err
		}
		out["signed_in"] = ok
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	census, err := db.Census()
	if err != nil {
		return err
	}
	syncs, err := db.Syncs()
	if err != nil {
		return err
	}
	var mine []any
	for _, row := range census {
		if row.Account != acct.Name {
			continue
		}
		mine = append(mine, row)
	}
	out["store"] = mine
	if c.asJSON {
		return emitJSON(out)
	}
	state := "signed out"
	if jar.SignedIn {
		state = "session cookie still valid"
	}
	if v, ok := out["signed_in"].(bool); ok {
		if v {
			state = "signed in (checked live)"
		} else {
			state = "signed out (checked live)"
		}
	}
	fmt.Printf("%s (%s, %s): %s\n", acct.Name, acct.Type, site.Base(), state)
	for _, row := range census {
		if row.Account != acct.Name {
			continue
		}
		last := syncs[row.Account+"/"+row.Market+"/orders"]
		when := "never synced"
		if !last.IsZero() {
			when = "synced " + last.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("  %-6s %5d orders (%s to %s), %d transactions, %d gift card, %d returns, %d summaries; %s\n",
			row.Market, row.Orders, row.Oldest, row.Newest, row.Transactions, row.GiftCard, row.Returns, row.Summaries, when)
	}
	if len(mine) == 0 {
		fmt.Println("  store is empty for this account (run `amz sync` or `amz import DIR`)")
	}
	return nil
}

func marketsCmd(args []string) error {
	c := flags("markets", "")
	if err := c.parse(args); err != nil {
		return err
	}
	if c.asJSON {
		return emitJSON(market.All())
	}
	fmt.Printf("%-6s %-20s %-16s %-4s %-6s %-6s %s\n", "CODE", "HOST", "COUNTRY", "CUR", "SHARED", "CAMEL", "GUARANTEE")
	for _, s := range market.All() {
		shared, camel := "no", "no"
		if s.ASINShared {
			shared = "yes"
		}
		if s.Camel != "" {
			camel = "yes"
		}
		fmt.Printf("%-6s %-20s %-16s %-4s %-6s %-6s %dy\n", s.Code, s.Host, s.Country, s.Currency, shared, camel, s.GuaranteeYears)
	}
	fmt.Printf("Default price sweep: %s\n", strings.Join(market.Default, ", "))
	return nil
}
