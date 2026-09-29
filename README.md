<h1 align="center">amz</h1>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/LICENSE-MIT-5C9E31?style=for-the-badge" alt="License"></a>
  <a href="https://github.com/jgalea"><img src="https://img.shields.io/badge/BUILT%20BY-JGALEA-8A2BE2?style=for-the-badge&logo=github&logoColor=white" alt="Built by jgalea"></a>
</p>

<p align="center"><strong>Your Amazon orders, refunds, prices and reviews across every EU store, from the terminal.</strong></p>

---

## Why this exists

Amazon has no customer-facing API. The order history is only reachable through the site, ten orders a page, one year at a time; invoices sit behind a popover; the same product costs a different amount on each storefront and a different amount again on a business account. Checking that every return came back as money means cross-reading three pages by hand.

`amz` drives a real Chrome profile you signed into yourself for the account pages, fetches public product pages anonymously for prices and reviews, and keeps everything in a local SQLite store so the analysis runs offline. It reads. It never buys, cancels, returns or submits anything.

## Install

```
go install github.com/jgalea/amz/cmd/amz@latest
```

Needs Go 1.26+ and a local Chrome or Chromium for the signed-in commands. Runs on macOS, Linux and Windows.

## Accounts

Each account is one Amazon login with its own Chrome profile and cookie jar under `~/.amz/accounts/NAME/`. An account has a type (personal or business), a delivery country, a home storefront and the other storefronts the same login uses.

```
amz accounts add home --type personal --country PT --home es
amz accounts add work --type business --country PT --home es --storefronts de,fr,it
amz login --account work                 # opens a window; sign in yourself
amz accounts                             # name, type, storefronts, session state
amz accounts remove work
```

With no `--account`, the only account on disk is used. With several and no name it stops and asks. `--account all` runs a command over every account, output labelled per account.

Accounts from an earlier amz (a directory with a cookie jar and a `market` file, no `account.json`) are recognised as personal accounts on that storefront and given an `account.json` in place the first time the new binary runs; the signed-in session is untouched.

## The store

`amz sync` reads an account's storefront pages into `~/.amz/amz.db`: orders and their items, the payments transactions, the returns centre, the gift card activity log and, with `--summaries`, the printable order page of every returned order (which carries Amazon's own refund total). A sync is incremental: it re-walks the last 60 days and stops once it reaches what it already has. `--full` walks everything.

```
amz sync                                 # this account, home storefront
amz sync --account all --market all      # every account, every storefront
amz sync --full --summaries              # first run on a new account
amz sync --only transactions,giftcard
amz import DIR --account home --market es    # load a directory written by dump
```

The read commands then work offline, and every one of them takes `--json` or `--csv`:

```
amz orders --since 2026-01-01
amz orders --grep cable --account all
amz orders --asin B0TEST0001
amz refunds                              # refunds; --all for every transaction
amz giftcard
amz audit --account all --since 2025-01-01
amz status                               # session state and what the store holds
```

`--live` on `orders`, `refunds` and `giftcard` reads the site first and updates the store. `dump --out DIR` still writes `orders.json`, `orders.csv`, `refunds.json`, `giftcard.json` and an `invoices/` folder for archiving; `invoices --out DIR` saves each order's printable summary as PDF plus every PDF the invoice popover links to, skipping files that already exist.

## The audit

For every order whose card carries return, refund, replacement or cancellation wording, `amz audit` adds up what was charged and what came back on the payments page and gift card log, and gives a verdict: `refunded` (within €5 of the charge, since Amazon deducts return shipping), `partial`, `no refund seen`, `no transactions` (typically cancelled before payment), or `newer than transactions`. When the order page's own Refund Total is in the store it wins over what the payment logs could be matched to. Refunds on orders with no return wording are listed separately as settled.

A charge or refund covering several orders is split evenly and marked `~`. Orders placed before the oldest loaded transaction are excluded and counted, because an unloaded period must never read as "no refund".

## Prices and listings

These read public product pages anonymously, one request every two seconds per storefront, cached for six hours under `~/.amz/cache/`. No account is needed.

```
amz price B0TEST0001                     # es, de, fr, it, nl, co.uk in EUR
amz price B0TEST0001 --markets all --match
amz price B0TEST0001 --session           # plus what each of your accounts is shown
amz history B0TEST0001 --markets es --period 1y
amz reviews B0TEST0001 --market de
amz seller B0TEST0001 --market es
amz buywith B0TEST0001
```

`price` gives the price on each storefront converted with the ECB daily rate, the used or Warehouse price when the page advertises one, Subscribe & Save, coupons and business quantity tiers when visible, and where Amazon priced the page for (it prices for the country it infers from your IP, so the column matters). The account's country decides which destination counts as "yours". `--match` searches by EAN, then by title and brand, on storefronts that do not list the ASIN, and marks the result as matched so you can check it is the same pack size; Amazon walls its search pages more aggressively than product pages, so it does not always get through. Every observation is recorded in the store.

`history` saves CamelCamelCamel's chart for the ASIN. Camel's pages sit behind a Cloudflare challenge, but the chart renderer is open and its legend prints the exact lowest, highest and current price with dates, so the PNG is the data.

`reviews` reads the dozen or so reviews Amazon renders on the public page (the paginated review pages need a sign-in and are never fetched) and prints evidence rather than a score: verified share, Vine share, date clustering, star-curve shape, near-duplicate bodies, repeat reviewers, cross-marketplace share, per-review prose features that lean machine-written, and candidate Community Guidelines breaches with the rule cited. A signal that could not be evaluated says so; an empty sample is an unknown, not a pass.

`seller` shows the buy box seller and, with `--session`, the full offers panel through your signed-in browser (Amazon does not serve it anonymously): every seller with rating and count, FBA or not, and flags for new sellers priced far below the buy box.

`buywith` reads the product through each of your accounts and ranks them by effective cost: the ex-VAT price for a business account that reclaims VAT, the shelf price otherwise (`--no-reclaim` when the purchase is not deductible).

## After the purchase

```
amz returns window --account all         # still returnable, soonest deadline first
amz returns window --alert --days 3      # notify when a window is about to close
amz warranty waterpik                    # find the item, seller, order page, guarantee left
amz chase --days 14                      # returns Amazon has had for 14+ days with no refund
amz return prepare 402-1234567-0000001 B0TEST0001 --reason defective --comment "stopped charging"
```

`returns window` uses the deadline the order card showed when there was one, else delivery plus 30 days (marked `~`). `warranty` applies the legal conformity guarantee of the account's country: two years across the EU, three in Spain and Portugal for goods bought since 2022, six in Ireland and the UK. `chase` drafts a claim text per order (order id, dates, RMA, amounts) into `~/.amz/claims/`; it never sends anything.

`return prepare` is the one command that touches a form. It opens a visible window, follows the order's return link, ticks the item, picks the reason that matches your words, types the comment, prints every step it took, and stops before the button that submits the return. You press it, or close the window.

## Money

```
amz spend --by month --since 2026-01-01
amz spend --by account
amz invoices export --quarter 2026Q3 --account work --to ~/Accounting
amz bank match ~/Downloads/revolut-august.csv
```

`spend` sums the order totals on the cards and what the audit ledger matched as refunded, per year, month, account, market or account/market. `invoices export` copies the invoice PDFs of every order in the quarter (from `dump` or `invoices` output; `--from DIR,DIR`, default `~/.amz/invoices`) into `<to>/<year>/Q<n>/Amazon/<account>/` and lists the orders that have no downloadable invoice; `--with-summary` copies the printable summary for those, renamed `NOT-AN-INVOICE`. The default destination is `invoices.dir` in `~/.amz/config.json`.

`bank match` reads a Revolut, N26 or Wise CSV export, keeps the Amazon lines, and pairs each with the Amazon transaction of the same amount within a few days. It reports bank charges with no Amazon charge behind them (double charges), bank refunds with no Amazon refund, and Amazon transactions in the statement period that never reached the bank.

## Account health

```
amz health --account all
amz health --alert
amz notices                              # scan the mailbox in config.json over IMAP
amz notices --dir ~/Mail/amazon-eml      # or a folder of .eml files
```

`health` gives the return rate by count and by value over rolling 3, 6 and 12 months per account and storefront, the trend of the short window against the long one, and a warning at 10% (warn) and 20% (critical). Amazon publishes no threshold; those are the levels people report being restricted at.

`notices` keeps the mails that matter for an account's standing: account protection, restriction, returns-activity warnings, orders cancelled by Amazon (not the ones you cancelled), refund refusals, policy notices. Only mail from an amazon.* sender counts, so a phishing mail with the same subject is ignored. IMAP settings live in `~/.amz/config.json` (`mail.host`, `mail.port`, `mail.user`, `mail.folder`) and the password in the environment variable named by `mail.password_env`; Gmail works with an app password on `imap.gmail.com:993`.

## Watches, alerts and the schedule

```
amz watch add B0TEST0001 --below 120 --markets es,de --used
amz watch list
amz watch run                            # check every watch, alert on hits
amz watch remove 3
amz notify config --macos=true --ntfy-topic my-topic --ntfy-token-env NTFY_TOKEN
amz notify config --telegram-chat 123456 --telegram-token-env TELEGRAM_BOT_TOKEN
amz notify test
amz schedule install                     # writes the launchd agents, prints the launchctl lines
amz schedule install --load --label-prefix com.example.
```

A watch fires when the lowest converted price across its storefronts (used offers included with `--used`) is at or below the threshold; a hit alerts once and then stays quiet for a day. Alerts go to whichever channels are set: a macOS notification banner, an ntfy topic (from your own machine, so ntfy's per-IP quota is yours), a Telegram chat. Tokens are read from the environment variables you name, never stored in the settings file.

`schedule install` writes four launchd agents (`sync --market all` daily, `watch run` every six hours, `returns window --alert` and `health --alert` daily) into `~/Library/LaunchAgents` and leaves loading them to you unless `--load` is given. Logs land in `~/.amz/logs/`. A local jobs registry that keys on the label prefix picks them up if you pass its prefix.

## MCP server

`amz mcp` serves the read commands as tools over stdio (Model Context Protocol), so an agent can ask for a price, an order, a warranty or the account's health the same way you do: `price`, `history`, `reviews`, `seller`, `buywith`, `orders`, `warranty`, `returns_window`, `audit`, `spend`, `health`, `markets`. `return prepare` is not exposed. With Claude Code:

```
claude mcp add --scope user amz -- amz mcp
```

## Pacing

Amazon signs a session out after a few hundred rapid page loads. `--delay` (default `2s`) is the pause between loads; raise it for a big first run. Headless runs can trip bot checks; pass `--show` to run with a visible window if a command comes back empty or signed out.

## How the session works

`amz login` starts Chrome with a dedicated profile and watches the address bar until it leaves the sign-in flow. It never types into the page. After a confirmed sign-in the cookie jar is read over the DevTools protocol and written to `~/.amz/accounts/NAME/cookies.json` (mode 600), then put back before each later run. Treat that file as a password: anyone holding it holds your account session. Delete it to sign out.

This is scraping, so it breaks when Amazon changes its markup. `amz page orders|transactions|returns|giftcard FILE` saves the rendered page so the selectors can be fixed against real HTML; the parsers are plain Go over that HTML with fixtures under `internal/amazon/testdata`.

## Configuration

| Variable | Default | What it does |
|---|---|---|
| `AMZ_CONFIG_DIR` | `~/.amz` | Where accounts, the store and the cache live |
| `AMZ_ACCOUNT` | the only account, else `default` | Account when `--account` is not given |
| `AMZ_MARKET` | the account's home storefront | Storefront when `--market` is not given |
| `AMZ_CHROME` | autodetected | Path to the Chrome or Chromium binary |

Storefronts: `es de fr it nl be ie pl se co.uk com` (`amz markets` lists them with currency and legal guarantee).

## Limits

- Reads only. It never places, cancels, returns or pays for anything.
- Archived orders are not walked.
- The gift card activity parser has been checked against one storefront's page.

## License

MIT. Not affiliated with or endorsed by Amazon. Amazon is a trademark of Amazon.com, Inc. or its affiliates.
