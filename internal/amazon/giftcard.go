package amazon

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// GiftCardEntry is one line of the gift card balance activity log.
// Refunds to gift card balance land here and nowhere else.
type GiftCardEntry struct {
	Date        string `json:"date,omitempty"`
	DateISO     string `json:"date_iso,omitempty"`
	Description string `json:"description,omitempty"`
	OrderID     string `json:"order_id,omitempty"`
	// Amount keeps the page's own sign: credits positive, spend negative.
	Amount string `json:"amount"`
}

var signedAmountRe = regexp.MustCompile(`^[+\-−]?\s*(?:[€$£]|[A-Z]{3})\s*[\d.,]+$|^[+\-−]?\s*[\d.,]+\s*(?:[€$£]|[A-Z]{3})$`)

const giftCardReadySelector = "table, [data-testid], #gc-activity, .gc-activity, #a-page"

// ParseGiftCard reads the activity log. A table layout is read row by
// row; otherwise the page's text is walked as date, description lines,
// amount, like the payments page. Neither layout has been checked
// against a live page yet.
func ParseGiftCard(doc *goquery.Document) []GiftCardEntry {
	doc.Find("script, style, noscript").Remove()
	if out := parseGiftCardTable(doc); len(out) > 0 {
		return out
	}
	return parseGiftCardLeaves(doc)
}

func parseGiftCardTable(doc *goquery.Document) []GiftCardEntry {
	var out []GiftCardEntry
	doc.Find("table tr").Each(func(_ int, tr *goquery.Selection) {
		var e GiftCardEntry
		var rest []string
		tr.Find("td, th").Each(func(_ int, td *goquery.Selection) {
			t := Text(td)
			switch {
			case t == "":
			case e.DateISO == "" && ParseDate(t) != "":
				e.Date, e.DateISO = t, ParseDate(t)
			case e.Amount == "" && signedAmountRe.MatchString(t):
				e.Amount = t
			default:
				rest = append(rest, t)
			}
		})
		if e.DateISO == "" || e.Amount == "" {
			return
		}
		e.Description = strings.Join(rest, " ")
		e.OrderID = orderIDRe.FindString(tr.Text())
		out = append(out, e)
	})
	return out
}

func parseGiftCardLeaves(doc *goquery.Document) []GiftCardEntry {
	var out []GiftCardEntry
	var cur *GiftCardEntry
	var desc []string
	doc.Find("body").Each(func(_ int, body *goquery.Selection) {
		for _, t := range Lines(body) {
			switch {
			case cur == nil && len(t) < 40 && ParseDate(t) != "":
				cur = &GiftCardEntry{Date: t, DateISO: ParseDate(t)}
				desc = nil
			case cur == nil:
			case signedAmountRe.MatchString(t):
				cur.Amount = t
				cur.Description = strings.Join(desc, " ")
				cur.OrderID = orderIDRe.FindString(cur.Description)
				out = append(out, *cur)
				cur = nil
			default:
				desc = append(desc, t)
			}
		}
	})
	return out
}

// LoadGiftCard opens the gift card balance page and parses its
// activity log.
func LoadGiftCard(ctx context.Context, site Site) ([]GiftCardEntry, Page, error) {
	return LoadGiftCardSince(ctx, site, "")
}

// LoadGiftCardSince stops following "Next" once a page holds entries
// older than sinceISO.
func LoadGiftCardSince(ctx context.Context, site Site, sinceISO string) ([]GiftCardEntry, Page, error) {
	var all []GiftCardEntry
	first := Page{}
	url := site.GiftCardURL()
	// The activity log is paged 15 at a time behind a "Next →" link that
	// carries an opaque cursor; follow it until it disappears.
	for pages := 0; pages < 500 && url != ""; pages++ {
		p, err := Load(ctx, url, 25*time.Second, giftCardReadySelector)
		if err != nil {
			return all, first, err
		}
		if IsSignInPage(p.URL) {
			return all, first, ErrSignedOut
		}
		if pages == 0 {
			first = p
		}
		doc, err := p.Doc()
		if err != nil {
			return all, first, err
		}
		got := ParseGiftCard(doc)
		all = append(all, got...)
		url = ""
		if sinceISO != "" {
			for _, g := range got {
				if g.DateISO != "" && g.DateISO < sinceISO {
					return all, first, nil
				}
			}
		}
		doc.Find("a[href*='/gc/balance'][href*='next=']").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			if strings.HasPrefix(Text(a), "Next") || strings.HasPrefix(Text(a), "Siguiente") {
				href, _ := a.Attr("href")
				url = site.Base() + href
				if strings.HasPrefix(href, "http") {
					url = href
				}
				return false
			}
			return true
		})
		time.Sleep(Delay)
	}
	return all, first, nil
}
