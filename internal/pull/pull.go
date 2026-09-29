// Package pull is `amz sync`: it reads an account's storefront pages
// into the store, incrementally.
package pull

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/browser"
	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/store"
)

// Overlap is how far back an incremental sync re-reads. Orders change
// state for weeks after they are placed (delivered, returned, refunded),
// so a sync always re-walks this much of the recent history.
const Overlap = 60 * 24 * time.Hour

type Options struct {
	// Full walks every year and every transaction instead of stopping at
	// the last sync.
	Full bool
	// Since overrides the incremental cutoff (YYYY-MM-DD).
	Since string
	// Only limits the sync to some of: orders, returns, transactions,
	// giftcard, summaries. Empty means all.
	Only []string
	// Summaries fetches the printable order page for returned orders
	// that do not have one yet, which is one page load per order.
	Summaries bool
	Headless  bool
	Log       func(string)
}

func (o Options) wants(what string) bool {
	if len(o.Only) == 0 {
		return true
	}
	for _, w := range o.Only {
		if w == what {
			return true
		}
	}
	return false
}

// Result is what one account/storefront sync did.
type Result struct {
	Account      string `json:"account"`
	Market       string `json:"market"`
	Orders       int    `json:"orders"`
	Transactions int    `json:"transactions_new"`
	GiftCard     int    `json:"giftcard_new"`
	Returns      int    `json:"returns"`
	Summaries    int    `json:"summaries_new"`
	SignedOut    bool   `json:"signed_out,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Session opens the account's Chrome profile with its saved cookies.
func Session(acct config.Account, headless bool, timeout time.Duration) (*browser.Session, error) {
	s, err := browser.Open(context.Background(), browser.Options{
		ProfileDir: acct.ProfileDir(),
		Headless:   headless,
		Timeout:    timeout,
	})
	if err != nil {
		return nil, err
	}
	if _, err := browser.LoadCookies(s.Ctx, acct.CookiePath()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "warning: could not restore the saved session: "+err.Error())
	}
	return s, nil
}

// Run syncs one account on one storefront. A signed-out session ends
// the run for that storefront with SignedOut set rather than an error,
// so the caller can carry on with the others.
func Run(db *store.Store, acct config.Account, sf market.Storefront, o Options) Result {
	res := Result{Account: acct.Name, Market: sf.Code}
	log := o.Log
	if log == nil {
		log = func(string) {}
	}
	s, err := Session(acct, o.Headless, 2*time.Hour)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer s.Close()
	site := amazon.Site{TLD: sf.Code}

	cutoff := ""
	if !o.Full {
		cutoff = o.Since
		if cutoff == "" {
			last := db.LastSync(acct.Name, sf.Code, "orders")
			if !last.IsZero() {
				cutoff = last.Add(-Overlap).Format("2006-01-02")
			}
		}
	}

	fail := func(err error) Result {
		if err == amazon.ErrSignedOut {
			res.SignedOut = true
			return res
		}
		res.Error = err.Error()
		return res
	}

	if o.wants("orders") {
		years, err := amazon.Years(s.Ctx, site)
		if err != nil {
			return fail(err)
		}
		for _, y := range years {
			if cutoff != "" && fmt.Sprintf("%d-12-31", y) < cutoff {
				continue
			}
			got, err := amazon.ListYearSince(s.Ctx, site, y, cutoff, func(page, total int) {
				log(fmt.Sprintf("%s/%s orders %d: page %d, %d orders", acct.Name, sf.Code, y, page, total))
			})
			if err != nil {
				return fail(err)
			}
			if err := db.UpsertOrders(acct.Name, sf.Code, sf.Currency, got); err != nil {
				res.Error = err.Error()
				return res
			}
			res.Orders += len(got)
			time.Sleep(amazon.Delay)
		}
		db.SetSync(acct.Name, sf.Code, "orders")
	}

	if o.wants("returns") {
		rets, _, err := amazon.LoadReturns(s.Ctx, site)
		if err != nil {
			return fail(err)
		}
		if err := db.UpsertReturns(acct.Name, sf.Code, rets); err != nil {
			res.Error = err.Error()
			return res
		}
		res.Returns = len(rets)
		db.SetSync(acct.Name, sf.Code, "returns")
		time.Sleep(amazon.Delay)
	}

	if o.wants("transactions") {
		tx, err := amazon.LoadTransactionsSince(s.Ctx, site, cutoff, func(n int) {
			log(fmt.Sprintf("%s/%s transactions: %d", acct.Name, sf.Code, n))
		})
		if err != nil {
			return fail(err)
		}
		n, err := db.UpsertTransactions(acct.Name, sf.Code, tx)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.Transactions = n
		db.SetSync(acct.Name, sf.Code, "transactions")
		time.Sleep(amazon.Delay)
	}

	if o.wants("giftcard") {
		gc, _, err := amazon.LoadGiftCardSince(s.Ctx, site, cutoff)
		if err != nil {
			return fail(err)
		}
		n, err := db.UpsertGiftCard(acct.Name, sf.Code, gc)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.GiftCard = n
		db.SetSync(acct.Name, sf.Code, "giftcard")
	}

	if o.Summaries && o.wants("summaries") {
		n, err := summaries(s.Ctx, db, acct, sf, log)
		if err != nil {
			return fail(err)
		}
		res.Summaries = n
		db.SetSync(acct.Name, sf.Code, "summaries")
	}
	return res
}

// summaries fetches the printable order page for orders with return or
// refund wording that have no saved summary yet.
func summaries(ctx context.Context, db *store.Store, acct config.Account, sf market.Storefront, log func(string)) (int, error) {
	orders, err := db.Orders(store.Query{Accounts: []string{acct.Name}, Markets: []string{sf.Code}})
	if err != nil {
		return 0, err
	}
	have, err := db.Summaries(acct.Name, sf.Code)
	if err != nil {
		return 0, err
	}
	site := amazon.Site{TLD: sf.Code}
	got := map[string]amazon.Summary{}
	for _, o := range orders {
		if _, ok := have[o.ID]; ok || !hasReturnWording(o) {
			continue
		}
		p, err := amazon.Load(ctx, site.PrintSummaryURL(o.ID), 20*time.Second, "body")
		if err != nil {
			return len(got), err
		}
		if amazon.IsSignInPage(p.URL) {
			return len(got), amazon.ErrSignedOut
		}
		doc, err := p.Doc()
		if err != nil {
			return len(got), err
		}
		sum := amazon.ParseSummary(doc)
		sum.OrderID = o.ID
		got[o.ID] = sum
		log(fmt.Sprintf("%s/%s summary %s: refund total %.2f", acct.Name, sf.Code, o.ID, sum.RefundTotal))
		if err := db.UpsertSummaries(acct.Name, sf.Code, map[string]amazon.Summary{o.ID: sum}); err != nil {
			return len(got), err
		}
		time.Sleep(amazon.Delay)
	}
	return len(got), nil
}

func hasReturnWording(o store.Order) bool {
	if len(o.Returns) > 0 {
		return true
	}
	l := strings.ToLower(o.Status)
	for _, k := range []string{"return", "refund", "cancel", "devol", "reembols", "rück", "erstatt", "retour", "rembours", "reso", "rimbors"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// SessionPage loads any URL in the account's signed-in Chrome and
// returns the rendered HTML, for reading the price that account is
// shown. ErrSignedOut when the site redirects to sign-in.
func SessionPage(acct config.Account, u string, headless bool) (string, string, error) {
	s, err := Session(acct, headless, 3*time.Minute)
	if err != nil {
		return "", "", err
	}
	defer s.Close()
	p, err := amazon.Load(s.Ctx, u, 20*time.Second, "#productTitle, #outOfStock, #dp")
	if err != nil {
		return "", "", err
	}
	if amazon.IsSignInPage(p.URL) {
		return "", p.URL, amazon.ErrSignedOut
	}
	return p.HTML, p.URL, nil
}

// SessionFetch loads pageURL in the signed-in Chrome and then fetches
// fragmentURL from inside that page, so the request carries the session
// exactly as the browser would send it. Amazon serves the offers panel
// this way but not to a bare anonymous request.
func SessionFetch(acct config.Account, pageURL, fragmentURL string, headless bool) ([]byte, error) {
	s, err := Session(acct, headless, 3*time.Minute)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	p, err := amazon.Load(s.Ctx, pageURL, 20*time.Second, "#productTitle, #outOfStock, #dp")
	if err != nil {
		return nil, err
	}
	if amazon.IsSignInPage(p.URL) {
		return nil, amazon.ErrSignedOut
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	site := amazon.Site{TLD: strings.TrimPrefix(u.Hostname(), "www.amazon.")}
	body, _, err := amazon.FetchInPage(s.Ctx, site, fragmentURL)
	return body, err
}
