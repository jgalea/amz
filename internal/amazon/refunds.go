package amazon

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// Transaction is one line on the payments transactions page.
type Transaction struct {
	Date    string `json:"date,omitempty"`
	DateISO string `json:"date_iso,omitempty"`
	Amount  string `json:"amount"`
	Method  string `json:"method,omitempty"`
	OrderID string `json:"order_id,omitempty"`
	// OrderIDs lists every order one charge covers; Amazon sometimes
	// bills two orders as a single card transaction.
	OrderIDs []string `json:"order_ids,omitempty"`
	// Merchant is the last descriptive line, usually the storefront
	// or seller name.
	Merchant string `json:"merchant,omitempty"`
	Status   string `json:"status,omitempty"`
	// Refund is true for money coming back: a positive amount or
	// refund wording on the line.
	Refund bool `json:"refund"`
}

var (
	amountRe   = regexp.MustCompile(`^[+\-−]?\s*(?:[A-Z]{3}\s*)?[\d.,]+\s*(?:€|\$|£|[A-Z]{3})?$|^[+\-−]?\s*(?:€|\$|£)\s*[\d.,]+$`)
	refundTxRe = regexp.MustCompile(`(?i)reembols|refund|rembours|rimbors|erstatt|devolu|credit|abono`)
)

// ParseTransactions walks the transactions page in document order,
// picking up each date header and the line items under it.
func ParseTransactions(doc *goquery.Document) []Transaction {
	if doc.Find(".apx-transactions-line-item-component-container").Length() == 0 {
		return parseTransactionRows(doc)
	}
	var out []Transaction
	date := ""
	doc.Find(".apx-transaction-date-container, .apx-transactions-line-item-component-container").Each(func(_ int, s *goquery.Selection) {
		if s.HasClass("apx-transaction-date-container") {
			date = Text(s)
			return
		}
		out = append(out, parseTransactionGroup(s, date)...)
	})
	return out
}

// parseTransactionGroup splits one container into transactions. Each
// starts at the block holding the amount; the blocks that follow, up
// to the next amount, describe it.
func parseTransactionGroup(box *goquery.Selection, date string) []Transaction {
	inner := box.Find(".a-box-inner").First()
	if inner.Length() == 0 {
		inner = box
	}
	var out []Transaction
	var cur *Transaction
	inner.Children().Each(func(_ int, block *goquery.Selection) {
		amount := Text(block.Find(".a-size-base-plus.a-text-bold, .a-text-bold.a-size-base-plus").First())
		if amount == "" {
			block.Find("span").EachWithBreak(func(_ int, sp *goquery.Selection) bool {
				if t := Text(sp); amountRe.MatchString(t) && sp.Children().Length() == 0 {
					amount = t
					return false
				}
				return true
			})
		}
		if amount != "" {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &Transaction{Date: date, DateISO: ParseDate(date), Amount: amount}
			for _, line := range Lines(block) {
				if line != amount && cur.Method == "" {
					cur.Method = line
				}
			}
			return
		}
		if cur == nil {
			return
		}
		for _, line := range Lines(block) {
			if id := orderIDRe.FindString(line); id != "" && cur.OrderID == "" {
				cur.OrderID = id
				continue
			}
			if refundTxRe.MatchString(line) {
				cur.Refund = true
			}
			cur.Merchant = line
		}
	})
	if cur != nil {
		out = append(out, *cur)
	}
	for i := range out {
		if strings.HasPrefix(out[i].Amount, "+") {
			out[i].Refund = true
		}
	}
	return out
}

var (
	rowDateRe   = regexp.MustCompile(`^\d{1,2} \p{L}{3,}\.? \d{4}$`)
	orderHashRe = regexp.MustCompile(`^(?:Order|Pedido)\s*(?:#|n\.º)\s*(\d{3}-\d{7}-\d{7})$`)
)

// parseTransactionRows reads the React layout (2026), where each row is a
// flat run of text: date, "·", merchant, card name, "••••", last four,
// "Order #…", signed amount, then a status such as Refunded or Charged.
func parseTransactionRows(doc *goquery.Document) []Transaction {
	doc.Find("script, style, noscript").Remove()
	var leaves []string
	var walk func(*goquery.Selection)
	walk = func(s *goquery.Selection) {
		s.Contents().Each(func(_ int, c *goquery.Selection) {
			if goquery.NodeName(c) == "#text" {
				if t := Clean(c.Text()); t != "" {
					leaves = append(leaves, t)
				}
				return
			}
			walk(c)
		})
	}
	walk(doc.Find("body"))

	var out []Transaction
	var cur *Transaction
	var prev string
	wantStatus := false
	for _, t := range leaves {
		switch {
		case rowDateRe.MatchString(t):
			if cur != nil && cur.Amount != "" {
				out = append(out, *cur)
			}
			cur = &Transaction{Date: t, DateISO: ParseDate(t)}
			wantStatus = false
		case cur == nil:
		case wantStatus:
			cur.Status = t
			if refundTxRe.MatchString(t) {
				cur.Refund = true
			}
			wantStatus = false
		case prev == "·" && cur.Merchant == "":
			cur.Merchant = t
		case t == "••••":
			cur.Method = prev
		case prev == "••••" && cur.Method != "":
			cur.Method += " ••••" + t
		case orderHashRe.MatchString(t):
			id := orderHashRe.FindStringSubmatch(t)[1]
			if cur.OrderID == "" {
				cur.OrderID = id
			}
			cur.OrderIDs = append(cur.OrderIDs, id)
		case cur.Amount == "" && amountRe.MatchString(t) && strings.ContainsAny(t, "€$£"):
			cur.Amount = t
			cur.Refund = strings.HasPrefix(t, "+")
			wantStatus = true
		}
		prev = t
	}
	if cur != nil && cur.Amount != "" {
		out = append(out, *cur)
	}
	return out
}

const (
	transactionsReadySelector = `.apx-transactions-line-item-component-container, .apx-transaction-date-container, .pmts-portal-root, [data-testid="method-details-number"]`
	loadMoreSelector          = `input[name*="NextPageNavigationEvent"], input[name*="DefaultNextPageNavigationEvent"], .apx-load-more input, button.apx-load-more`
)

// LoadTransactions opens the payments page and keeps loading more until
// the list stops growing, then returns everything on it.
func LoadTransactions(ctx context.Context, site Site, progress func(n int)) ([]Transaction, error) {
	return LoadTransactionsSince(ctx, site, "", progress)
}

// LoadTransactionsSince stops loading more once the list reaches rows
// older than sinceISO; the rows already loaded are still all returned.
func LoadTransactionsSince(ctx context.Context, site Site, sinceISO string, progress func(n int)) ([]Transaction, error) {
	p, err := Load(ctx, site.TransactionsURL(), 25*time.Second, transactionsReadySelector)
	if err != nil {
		return nil, err
	}
	if IsSignInPage(p.URL) {
		return nil, ErrSignedOut
	}
	// The page offers a "load more" control; when it is absent, scroll
	// the window and any scrollable container to trigger the next batch.
	moreJS := `(() => {
  const btn = document.querySelector(` + jsString(loadMoreSelector) + `);
  if (btn && !btn.disabled && btn.offsetParent !== null) { btn.click(); return "click"; }
  const rows = [...document.querySelectorAll('[data-testid="text"]')].filter(e => /^[+\-−]\s*[€$£]/.test(e.textContent.trim()));
  if (rows.length) rows[rows.length - 1].scrollIntoView({block: "end"});
  window.scrollBy(0, -200);
  window.scrollTo(0, document.body.scrollHeight);
  for (const el of document.querySelectorAll("div")) {
    if (el.scrollHeight > el.clientHeight + 50 && getComputedStyle(el).overflowY.match(/auto|scroll/)) {
      el.scrollTop = el.scrollHeight;
    }
  }
  return "scroll";
})()`
	// The list is virtualised: rows scrolled far enough away are dropped
	// from the DOM, so every pass is parsed and merged, not just the end.
	var all []Transaction
	seen := map[string]int{}
	merge := func() error {
		var html string
		if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html)); err != nil {
			return err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			return err
		}
		// Two identical rows in one snapshot are two real transactions;
		// count them per snapshot so a later pass doesn't add them twice.
		inSnap := map[string]int{}
		for _, t := range ParseTransactions(doc) {
			k := fmt.Sprintf("%s|%s|%s|%v|%s", t.DateISO, t.Amount, t.Method, t.OrderIDs, t.Status)
			inSnap[k]++
			if inSnap[k] > seen[k] {
				seen[k] = inSnap[k]
				all = append(all, t)
			}
		}
		return nil
	}
	last, stable := -1, 0
	for pass := 0; pass < 2000; pass++ {
		if err := merge(); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(len(all))
		}
		if len(all) == last {
			stable++
			if stable >= 15 {
				break
			}
		} else {
			stable = 0
		}
		last = len(all)
		if sinceISO != "" && len(all) > 0 {
			oldest := all[0].DateISO
			for _, t := range all {
				if t.DateISO != "" && t.DateISO < oldest {
					oldest = t.DateISO
				}
			}
			if oldest != "" && oldest < sinceISO {
				break
			}
		}
		var how string
		if err := chromedp.Run(ctx, chromedp.Evaluate(moreJS, &how)); err != nil {
			return nil, err
		}
		time.Sleep(2 * time.Second)
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].DateISO > all[b].DateISO })
	return all, nil
}

// Return is one entry on the returns centre history page.
type Return struct {
	OrderID string   `json:"order_id"`
	Title   string   `json:"title,omitempty"`
	Status  string   `json:"status,omitempty"`
	Lines   []string `json:"lines,omitempty"`
}

var returnStatusRe = regexp.MustCompile(`(?i)reembols|refund|devoluci|devolvid|return|retour|rembours|rimbors|erstatt|rückgabe|complet|pendiente|pending|recibid|received|cerrad|closed|cancel`)

// ParseReturns reads the returns history page. The layout is not
// documented anywhere, so this is deliberately loose: the smallest
// block that mentions exactly one order number together with a product
// link or return wording becomes an entry, and its status is the first
// line with return or refund wording.
func ParseReturns(doc *goquery.Document) []Return {
	var out []Return
	seen := map[string]bool{}
	doc.Find("div, li, article, section").Each(func(_ int, box *goquery.Selection) {
		id, ok := returnBlock(box)
		if !ok || seen[id] {
			return
		}
		// A qualifying child means a tighter block exists; take that one.
		smaller := false
		box.Children().EachWithBreak(func(_ int, c *goquery.Selection) bool {
			if _, ok := returnBlock(c); ok {
				smaller = true
			}
			return !smaller
		})
		if smaller {
			return
		}
		seen[id] = true
		r := Return{OrderID: id}
		r.Title = Text(box.Find("a[href*='/dp/'], a[href*='/gp/product/'], .a-truncate-full").First())
		for _, line := range Lines(box) {
			if strings.Contains(line, id) || line == r.Title {
				continue
			}
			r.Lines = append(r.Lines, line)
			if r.Status == "" && returnStatusRe.MatchString(line) {
				r.Status = line
			}
		}
		out = append(out, r)
	})
	return out
}

// returnBlock reports whether a block mentions exactly one order and
// says something about it beyond the number.
func returnBlock(s *goquery.Selection) (string, bool) {
	ids := orderIDRe.FindAllString(s.Text(), -1)
	if len(ids) == 0 {
		return "", false
	}
	for _, x := range ids[1:] {
		if x != ids[0] {
			return "", false
		}
	}
	if s.Find("a[href*='/dp/'], a[href*='/gp/product/']").Length() > 0 {
		return ids[0], true
	}
	for _, line := range Lines(s) {
		if !strings.Contains(line, ids[0]) && returnStatusRe.MatchString(line) {
			return ids[0], true
		}
	}
	return "", false
}

// LoadReturns opens the returns centre history and parses it.
func LoadReturns(ctx context.Context, site Site) ([]Return, Page, error) {
	p, err := Load(ctx, site.ReturnsURL(), 25*time.Second, "main, #a-page")
	if err != nil {
		return nil, p, err
	}
	if IsSignInPage(p.URL) {
		return nil, p, ErrSignedOut
	}
	doc, err := p.Doc()
	if err != nil {
		return nil, p, err
	}
	return ParseReturns(doc), p, nil
}
