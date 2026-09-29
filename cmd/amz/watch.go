package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/fetch"
	"github.com/jgalea/amz/internal/notify"
	"github.com/jgalea/amz/internal/schedule"
	"github.com/jgalea/amz/internal/store"
	"github.com/jgalea/amz/internal/watch"
)

func init() {
	register("watch", "watch add ASIN --below N | list | remove ID | run: price watches with alerts", watchCmd)
	register("notify", "notify test [MESSAGE] | notify config: alert channels", notifyCmd)
	register("schedule", "schedule install: launchd agents for sync, watches, return windows and health", scheduleCmd)
}

func watchCmd(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "add":
		return watchAdd(args)
	case "list":
		c := flags("watch list", "")
		if err := c.parse(args); err != nil {
			return err
		}
		db, err := openStore()
		if err != nil {
			return err
		}
		defer db.Close()
		ws, err := db.Watches()
		if err != nil {
			return err
		}
		if c.asJSON {
			return emitJSON(ws)
		}
		if len(ws) == 0 {
			fmt.Println("no watches (add one with `amz watch add ASIN --below 120`)")
			return nil
		}
		fmt.Printf("%-4s %-11s %-14s %8s %5s %10s %-6s %-17s %s\n", "ID", "ASIN", "MARKETS", "BELOW", "USED", "LAST", "WHERE", "CHECKED", "TITLE")
		for _, w := range ws {
			used := ""
			if w.Used {
				used = "yes"
			}
			last := "-"
			if w.LastPrice > 0 {
				last = fmt.Sprintf("%.2f", w.LastPrice)
			}
			checked := ""
			if w.LastChecked != "" {
				if t, err := time.Parse(time.RFC3339, w.LastChecked); err == nil {
					checked = t.Local().Format("2006-01-02 15:04")
				}
			}
			fmt.Printf("%-4d %-11s %-14s %8.2f %5s %10s %-6s %-17s %s\n", w.ID, w.ASIN, w.Markets, w.Below, used, last, w.LastMarket, checked, truncate(w.Title, 50))
		}
		return nil
	case "remove":
		c := flags("watch remove", "ID")
		if err := c.parse(args); err != nil {
			return err
		}
		if len(c.args) != 1 {
			return fmt.Errorf("watch remove needs the watch id (see `amz watch list`)")
		}
		id, err := strconv.ParseInt(c.args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a watch id", c.args[0])
		}
		db, err := openStore()
		if err != nil {
			return err
		}
		defer db.Close()
		ok, err := db.RemoveWatch(id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no watch %d", id)
		}
		fmt.Printf("removed watch %d\n", id)
		return nil
	case "run":
		return watchRun(args)
	}
	return fmt.Errorf("watch: unknown subcommand %q (add, list, remove, run)", sub)
}

func watchAdd(args []string) error {
	c := flags("watch add", "ASIN|URL --below PRICE [--markets es,de | all] [--used]")
	var below float64
	var markets string
	var used bool
	c.fs.Float64Var(&below, "below", 0, "alert when the price (in EUR) is at or below this")
	c.fs.StringVar(&markets, "markets", "", "storefronts to watch (default es,de,fr,it,nl,co.uk)")
	c.fs.BoolVar(&used, "used", false, "count used and Warehouse offers too")
	if err := c.parse(args); err != nil {
		return err
	}
	asin, err := asinArg(c.args)
	if err != nil {
		return err
	}
	if below <= 0 {
		return fmt.Errorf("watch add needs --below PRICE")
	}
	sfs, err := marketsFlag(c, markets)
	if err != nil {
		return err
	}
	var codes []string
	for _, s := range sfs {
		codes = append(codes, s.Code)
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := db.AddWatch(store.Watch{ASIN: asin, Markets: strings.Join(codes, ","), Below: below, Used: used})
	if err != nil {
		return err
	}
	fmt.Printf("watch %d: %s below %.2f EUR on %s\n", id, asin, below, strings.Join(codes, ","))
	return nil
}

func watchRun(args []string) error {
	c := flags("watch run", "[--refresh] [--quiet]")
	var refresh, quiet bool
	c.fs.BoolVar(&refresh, "refresh", true, "bypass the page cache (default on: a watch wants fresh prices)")
	c.fs.BoolVar(&quiet, "quiet", false, "check and record, but send no alerts")
	if err := c.parse(args); err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	ws, err := db.Watches()
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		fmt.Fprintln(os.Stderr, "no watches")
		return nil
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	fetch.MinDelay = c.delay
	country := buyerCountry(c)
	var outcomes []watch.Outcome
	for i, w := range ws {
		o := watch.Check(w, country, refresh)
		if o.Error == "" {
			notified := false
			if !quiet && watch.ShouldNotify(o, time.Now()) {
				for _, r := range notify.Send(settings.Notify, "amz: price drop", watch.Message(o)) {
					if r.OK {
						notified = true
					} else {
						fmt.Fprintf(os.Stderr, "notify %s: %s\n", r.Channel, r.Error)
					}
				}
			}
			o.Notified = notified
			if err := db.TouchWatch(w.ID, o.Lowest, o.Market, o.Title, notified); err != nil {
				return err
			}
		}
		outcomes = append(outcomes, o)
		if i < len(ws)-1 {
			time.Sleep(c.delay)
		}
	}
	if c.asJSON {
		return emitJSON(outcomes)
	}
	for _, o := range outcomes {
		switch {
		case o.Error != "":
			fmt.Printf("%-4d %-11s error: %s\n", o.Watch.ID, o.Watch.ASIN, o.Error)
		case o.Hit:
			state := "HIT"
			if o.Notified {
				state += ", alerted"
			}
			fmt.Printf("%-4d %-11s %8.2f on %-6s %s  %s\n", o.Watch.ID, o.Watch.ASIN, o.Lowest, o.Market, state, truncate(o.Title, 50))
		default:
			fmt.Printf("%-4d %-11s %8.2f on %-6s above %.2f  %s\n", o.Watch.ID, o.Watch.ASIN, o.Lowest, o.Market, o.Watch.Below, truncate(o.Title, 50))
		}
	}
	return nil
}

func notifyCmd(args []string) error {
	sub := "config"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	c := flags("notify "+sub, "test [MESSAGE] | config [--macos=true|false] [--ntfy-topic T] [--ntfy-server URL] [--ntfy-token-env VAR] [--telegram-chat ID] [--telegram-token-env VAR]")
	var macos, ntfyTopic, ntfyServer, ntfyToken, tgChat, tgToken string
	c.fs.StringVar(&macos, "macos", "", "true or false")
	c.fs.StringVar(&ntfyTopic, "ntfy-topic", "", "ntfy topic (empty string to clear)")
	c.fs.StringVar(&ntfyServer, "ntfy-server", "", "ntfy server (default https://ntfy.sh)")
	c.fs.StringVar(&ntfyToken, "ntfy-token-env", "", "environment variable holding the ntfy token")
	c.fs.StringVar(&tgChat, "telegram-chat", "", "Telegram chat id")
	c.fs.StringVar(&tgToken, "telegram-token-env", "", "environment variable holding the bot token")
	if err := c.parse(args); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	switch sub {
	case "test":
		msg := "amz notifications work"
		if len(c.args) > 0 {
			msg = strings.Join(c.args, " ")
		}
		results := notify.Send(settings.Notify, "amz", msg)
		if c.asJSON {
			return emitJSON(results)
		}
		if len(results) == 0 {
			fmt.Println("no channel is configured (run `amz notify config --macos=true` or set an ntfy topic / Telegram chat)")
			return nil
		}
		for _, r := range results {
			if r.OK {
				fmt.Printf("%s: sent\n", r.Channel)
			} else {
				fmt.Printf("%s: %s\n", r.Channel, r.Error)
			}
		}
		return nil
	case "config":
		// Only flags given on the command line change the file, so an
		// empty value can clear a field without wiping the others.
		changed := false
		c.fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "macos":
				settings.Notify.MacOS = macos == "true"
			case "ntfy-topic":
				settings.Notify.Ntfy.Topic = ntfyTopic
			case "ntfy-server":
				settings.Notify.Ntfy.Server = ntfyServer
			case "ntfy-token-env":
				settings.Notify.Ntfy.TokenEnv = ntfyToken
			case "telegram-chat":
				settings.Notify.Telegram.ChatID = tgChat
			case "telegram-token-env":
				settings.Notify.Telegram.TokenEnv = tgToken
			default:
				return
			}
			changed = true
		})
		if changed {
			if err := settings.Save(); err != nil {
				return err
			}
		}
		if c.asJSON {
			return emitJSON(settings.Notify)
		}
		n := settings.Notify
		fmt.Printf("macos: %v\n", n.MacOS)
		fmt.Printf("ntfy: topic=%q server=%q token_env=%q\n", n.Ntfy.Topic, n.Ntfy.Server, n.Ntfy.TokenEnv)
		fmt.Printf("telegram: chat_id=%q token_env=%q\n", n.Telegram.ChatID, n.Telegram.TokenEnv)
		fmt.Printf("settings file: %s\n", filepath.Join(config.Dir(), "config.json"))
		return nil
	}
	return fmt.Errorf("notify: unknown subcommand %q (test, config)", sub)
}

func scheduleCmd(args []string) error {
	c := flags("schedule", "install [--dir ~/Library/LaunchAgents] [--label-prefix com.amz.] [--load] [--account NAME]")
	var dir, prefix string
	var load bool
	c.fs.StringVar(&dir, "dir", "", "where to write the plists (default ~/Library/LaunchAgents)")
	c.fs.StringVar(&prefix, "label-prefix", "com.amz.", "launchd label prefix (a local jobs registry may key on it)")
	c.fs.BoolVar(&load, "load", false, "also run launchctl load on each agent")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.args) == 0 || c.args[0] != "install" {
		return fmt.Errorf("usage: amz schedule install [--dir DIR] [--load]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if dir == "" {
		dir = filepath.Join(home, "Library", "LaunchAgents")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	account := ""
	if c.account != "" && c.account != "all" {
		account = c.account
	}
	env := map[string]string{"PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"}
	if d := os.Getenv("AMZ_CONFIG_DIR"); d != "" {
		env["AMZ_CONFIG_DIR"] = d
	}
	logDir := filepath.Join(config.Dir(), "logs")
	jobs := schedule.Jobs(account)
	paths, err := schedule.Install(dir, prefix, binary, logDir, jobs, env)
	if err != nil {
		return err
	}
	if c.asJSON {
		return emitJSON(map[string]any{"plists": paths, "loaded": load, "log_dir": logDir})
	}
	for i, p := range paths {
		fmt.Printf("wrote %s (every %s)\n", p, jobs[i].Interval)
	}
	if load {
		for _, p := range paths {
			out, err := exec.Command("launchctl", "load", "-w", p).CombinedOutput()
			if err != nil {
				return fmt.Errorf("launchctl load %s: %s", p, strings.TrimSpace(string(out)))
			}
			fmt.Printf("loaded %s\n", filepath.Base(p))
		}
	} else {
		fmt.Println("Not loaded. To activate:")
		for _, p := range paths {
			fmt.Printf("  launchctl load -w %s\n", p)
		}
	}
	// A custom --dir is a dry run or a test; only a real install is
	// worth announcing on the local jobs feed.
	if reg := schedule.Registry(); reg != "" && dir == filepath.Join(home, "Library", "LaunchAgents") {
		if err := schedule.Announce(reg, "amz", fmt.Sprintf("installed %d launchd agents with prefix %s", len(paths), prefix)); err == nil {
			fmt.Printf("announced on the jobs registry at %s\n", reg)
		}
	}
	fmt.Printf("Logs go to %s. Alerts use the channels in `amz notify config`.\n", logDir)
	return nil
}
