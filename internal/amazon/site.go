// Package amazon reads the signed-in customer account's orders,
// invoices and refunds off the Amazon retail site.
package amazon

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// Site is one Amazon marketplace, addressed by its domain suffix
// ("es", "co.uk", "de", "com").
type Site struct {
	TLD string
}

func (s Site) Base() string {
	return "https://www.amazon." + s.TLD
}

func (s Site) OrdersURL(year, startIndex int) string {
	u := s.Base() + "/gp/css/order-history"
	q := url.Values{}
	if year > 0 {
		q.Set("timeFilter", fmt.Sprintf("year-%d", year))
	}
	if startIndex > 0 {
		q.Set("startIndex", fmt.Sprint(startIndex))
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (s Site) OrderDetailsURL(id string) string {
	return s.Base() + "/gp/your-account/order-details?orderID=" + url.QueryEscape(id)
}

func (s Site) PrintSummaryURL(id string) string {
	return s.Base() + "/gp/css/summary/print.html?orderID=" + url.QueryEscape(id)
}

func (s Site) InvoicePopoverURL(id string) string {
	return s.Base() + "/gp/shared-cs/ajax/invoice/invoice.html?orderId=" + url.QueryEscape(id)
}

func (s Site) TransactionsURL() string {
	return s.Base() + "/cpe/yourpayments/transactions"
}

func (s Site) ReturnsURL() string {
	return s.Base() + "/spr/returns/history"
}

func (s Site) GiftCardURL() string {
	return s.Base() + "/gc/balance"
}

// Delay is the pause between consecutive page loads. Amazon signs a
// session out after a few hundred rapid loads, so callers set this from
// the --delay flag rather than hammering the site.
var Delay = 1500 * time.Millisecond

// Absolute turns a relative href from a page into a full URL on this site.
// Owns reports whether u is an https URL on this storefront's own host,
// the only place a signed-in fetch may go.
func (s Site) Owns(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Port() != "" {
		return false
	}
	h := p.Hostname()
	return h == "www.amazon."+s.TLD || strings.HasSuffix(h, ".amazon."+s.TLD)
}

func (s Site) Absolute(href string) string {
	if href == "" || strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return s.Base() + href
}

// IsSignInPage reports whether a URL is one of Amazon's sign-in,
// verification or captcha pages.
func IsSignInPage(u string) bool {
	for _, s := range []string{"/ap/signin", "/ap/cvf", "/ap/mfa", "/ap/challenge", "/errors/validateCaptcha", "/ap/register"} {
		if strings.Contains(u, s) {
			return true
		}
	}
	return false
}

// Page is a loaded page: its final URL and rendered HTML.
type Page struct {
	URL  string
	HTML string
}

func (p Page) Doc() (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(p.HTML))
}

// Load navigates to u and waits, up to wait, until one of the selectors
// is present in the DOM (or the browser has landed on a sign-in page),
// then returns the rendered page. A timeout is not an error: the page
// is returned as it is, and the caller decides whether it is usable.
func Load(ctx context.Context, u string, wait time.Duration, selectors ...string) (Page, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate(u)); err != nil {
		return Page{}, err
	}
	deadline := time.Now().Add(wait)
	js := "(() => !!document.querySelector(" + jsString(strings.Join(selectors, ", ")) + "))()"
	for {
		var current string
		if err := chromedp.Run(ctx, chromedp.Location(&current)); err != nil {
			return Page{}, err
		}
		if IsSignInPage(current) {
			break
		}
		if len(selectors) > 0 {
			var ready bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ready)); err == nil && ready {
				break
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	var p Page
	err := chromedp.Run(ctx,
		chromedp.Sleep(750*time.Millisecond),
		chromedp.Location(&p.URL),
		chromedp.OuterHTML("html", &p.HTML),
	)
	return p, err
}

// LoggedIn loads the order list and reports whether it rendered as an
// order list rather than a sign-in page.
func LoggedIn(ctx context.Context, site Site) (bool, error) {
	p, err := Load(ctx, site.OrdersURL(0, 0), 20*time.Second, ordersReadySelector)
	if err != nil {
		return false, err
	}
	if IsSignInPage(p.URL) {
		return false, nil
	}
	doc, err := p.Doc()
	if err != nil {
		return false, err
	}
	return doc.Find(ordersReadySelector).Length() > 0, nil
}

// WaitForLogin opens the order list once and then watches the address
// bar without touching it, so a sign-in in progress is never reloaded
// out from under the person doing it. It returns when the session is
// live, when done fires, or when ctx is cancelled.
func WaitForLogin(ctx context.Context, site Site, done <-chan struct{}) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(site.OrdersURL(0, 0))); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			ok, err := LoggedIn(ctx, site)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("that profile still looks signed out")
			}
			return nil
		default:
		}

		var current string
		if err := chromedp.Run(ctx, chromedp.Location(&current)); err != nil {
			return err
		}
		if current != "" && !IsSignInPage(current) && (strings.Contains(current, "/order-history") || strings.Contains(current, "/your-orders/")) {
			ok, err := LoggedIn(ctx, site)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
}

// Text returns the element's text with whitespace collapsed.
func Text(s *goquery.Selection) string {
	return Clean(s.Text())
}

func Clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Lines splits an element's text into its visible lines, one per
// block-level child, which is close to what a person sees on the page.
func Lines(s *goquery.Selection) []string {
	var out []string
	s.Contents().Each(func(_ int, c *goquery.Selection) {
		if goquery.NodeName(c) == "#text" {
			if t := Clean(c.Text()); t != "" {
				out = append(out, t)
			}
			return
		}
		if c.Children().Length() == 0 {
			if t := Clean(c.Text()); t != "" {
				out = append(out, t)
			}
			return
		}
		out = append(out, Lines(c)...)
	})
	return out
}

func jsString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
