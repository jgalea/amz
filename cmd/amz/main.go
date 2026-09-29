package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/store"
)

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func register(name, summary string, run func(args []string) error) {
	commands = append(commands, command{name, summary, run})
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "amz: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		return nil
	}
	name, rest := args[0], args[1:]
	for _, c := range commands {
		if c.name == name {
			return c.run(rest)
		}
	}
	return fmt.Errorf("unknown command %q (try `amz help`)", name)
}

func usage() {
	fmt.Println("amz - your Amazon accounts, prices and listings from the command line")
	fmt.Println()
	fmt.Println("Usage: amz COMMAND [flags]")
	fmt.Println()
	sorted := append([]command(nil), commands...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	for _, c := range sorted {
		fmt.Printf("  %-16s %s\n", c.name, c.summary)
	}
	fmt.Println()
	fmt.Println("Every command takes --help. Common flags: --account NAME|all, --market CODE|all, --json, --csv, --delay D.")
	fmt.Println()
	fmt.Println("Environment: AMZ_CONFIG_DIR (default ~/.amz), AMZ_ACCOUNT, AMZ_MARKET, AMZ_CHROME")
}

// common is the flag set every command shares.
type common struct {
	fs      *flag.FlagSet
	account string
	market  string
	asJSON  bool
	asCSV   bool
	delay   time.Duration
	show    bool
	// args are the positional arguments, wherever they sat among the
	// flags.
	args []string
}

func flags(name, synopsis string) *common {
	c := &common{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(os.Stderr)
	c.fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: amz %s %s\n\n", name, synopsis)
		c.fs.PrintDefaults()
	}
	c.fs.StringVar(&c.account, "account", os.Getenv("AMZ_ACCOUNT"), "account name, or all")
	c.fs.StringVar(&c.market, "market", os.Getenv("AMZ_MARKET"), "storefront code (es, de, co.uk, ...), or all")
	c.fs.BoolVar(&c.asJSON, "json", false, "emit JSON")
	c.fs.BoolVar(&c.asCSV, "csv", false, "emit CSV")
	c.fs.DurationVar(&c.delay, "delay", 2*time.Second, "pause between page loads")
	c.fs.BoolVar(&c.show, "show", false, "run Chrome with a visible window")
	return c
}

func (c *common) parse(args []string) error {
	rest := args
	for {
		if err := c.fs.Parse(rest); err != nil {
			return err
		}
		if c.fs.NArg() == 0 {
			break
		}
		c.args = append(c.args, c.fs.Arg(0))
		rest = c.fs.Args()[1:]
	}
	if c.delay < 0 {
		return fmt.Errorf("--delay must not be negative")
	}
	amazon.Delay = c.delay
	return nil
}

// accounts expands --account into the accounts to work on.
func (c *common) accounts() ([]config.Account, error) {
	return config.Selected(c.account)
}

func (c *common) one() (config.Account, error) {
	if c.account == "all" {
		return config.Account{}, fmt.Errorf("this command works on one account; name it with --account")
	}
	return config.Resolve(c.account)
}

// storefronts expands --market for an account: a code, "all" (every
// storefront the account lists), or empty (the account's home).
func (c *common) storefronts(a config.Account) ([]market.Storefront, error) {
	switch strings.ToLower(c.market) {
	case "":
		s, err := market.Get(a.Home)
		if err != nil {
			return nil, err
		}
		return []market.Storefront{s}, nil
	case "all":
		return a.Markets()
	}
	return market.Resolve(c.market)
}

// filter is a store query built from the common flags.
func (c *common) filter() (store.Query, error) {
	var q store.Query
	if c.account != "all" {
		a, err := config.Resolve(c.account)
		if err != nil {
			return q, err
		}
		q.Accounts = []string{a.Name}
	}
	if c.market != "" && c.market != "all" {
		sfs, err := market.Resolve(c.market)
		if err != nil {
			return q, err
		}
		for _, s := range sfs {
			q.Markets = append(q.Markets, s.Code)
		}
	}
	return q, nil
}

func openStore() (*store.Store, error) {
	if err := config.Migrate(); err != nil {
		return nil, err
	}
	return store.Open(config.DBPath())
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func logLine(w io.Writer) func(string) {
	return func(s string) { fmt.Fprintln(w, s) }
}

func isoDate(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", fmt.Errorf("%q is not a YYYY-MM-DD date", s)
	}
	return s, nil
}
