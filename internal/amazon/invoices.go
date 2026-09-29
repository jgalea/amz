package amazon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type InvoiceLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ParseInvoiceLinks reads the downloadable documents out of the
// "Invoice" popover. The printable summary and the popover itself are
// left out; "request an invoice" links are not documents.
func ParseInvoiceLinks(site Site, doc *goquery.Document) []InvoiceLink {
	var out []InvoiceLink
	seen := map[string]bool{}
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if !isDocumentLink(href) {
			return
		}
		u := site.Absolute(href)
		if seen[u] {
			return
		}
		seen[u] = true
		out = append(out, InvoiceLink{Label: Text(a), URL: u})
	})
	return out
}

func isDocumentLink(href string) bool {
	h := strings.ToLower(href)
	for _, skip := range []string{"print.html", "/invoice/invoice.html", "request", "javascript:"} {
		if strings.Contains(h, skip) {
			return false
		}
	}
	path := strings.SplitN(h, "?", 2)[0]
	if strings.Contains(path, "/documents/download/") || strings.HasSuffix(path, ".pdf") {
		return true
	}
	return strings.Contains(path, "invoice") && (strings.Contains(path, "download") || strings.Contains(path, "/documents/"))
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SaveInvoices writes, for each order, the printable order summary as
// PDF (and its HTML) plus every PDF the invoice popover links to.
// Files that already exist are left alone, so a rerun only fetches
// what is missing. log, if not nil, gets one line per order.
func SaveInvoices(ctx context.Context, site Site, orders []Order, dir string, log func(string)) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if log == nil {
		log = func(string) {}
	}
	for _, o := range orders {
		base := o.DateISO
		if base == "" {
			base = unsafeFile.ReplaceAllString(o.Date, "-")
		}
		if base == "" {
			base = "undated"
		}
		base += "_" + o.ID
		summaryPDF := filepath.Join(dir, base+"_summary.pdf")
		summaryHTML := filepath.Join(dir, base+"_summary.html")

		if !exists(summaryPDF) {
			p, err := Load(ctx, site.PrintSummaryURL(o.ID), 20*time.Second, "body")
			if err != nil {
				return err
			}
			if IsSignInPage(p.URL) {
				return ErrSignedOut
			}
			pdf, err := printToPDF(ctx)
			if err != nil {
				return fmt.Errorf("order %s: printing summary: %w", o.ID, err)
			}
			if err := os.WriteFile(summaryHTML, []byte(p.HTML), 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(summaryPDF, pdf, 0o600); err != nil {
				return err
			}
			log(o.ID + "  summary saved")
		} else {
			log(o.ID + "  summary exists")
		}

		popover := o.InvoiceURL
		if popover == "" {
			popover = site.InvoicePopoverURL(o.ID)
		}
		// The popover is fetched from inside the page, so it goes out
		// with the session's cookies. We are on the site already.
		body, _, err := FetchInPage(ctx, popover)
		if err != nil {
			log(o.ID + "  invoice popover: " + err.Error())
			continue
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		links := ParseInvoiceLinks(site, doc)
		if len(links) == 0 {
			log(o.ID + "  no downloadable invoice")
			continue
		}
		for i, l := range links {
			out := filepath.Join(dir, fmt.Sprintf("%s_invoice-%d.pdf", base, i+1))
			if exists(out) {
				continue
			}
			data, ctype, err := FetchInPage(ctx, l.URL)
			if err != nil {
				log(fmt.Sprintf("%s  invoice %d: %s", o.ID, i+1, err))
				continue
			}
			if !strings.Contains(ctype, "pdf") && !strings.HasPrefix(string(data), "%PDF") {
				// Not a PDF: usually an HTML invoice page. Print it instead.
				p, err := Load(ctx, l.URL, 20*time.Second, "body")
				if err != nil {
					return err
				}
				if IsSignInPage(p.URL) {
					return ErrSignedOut
				}
				data, err = printToPDF(ctx)
				if err != nil {
					log(fmt.Sprintf("%s  invoice %d: %s", o.ID, i+1, err))
					continue
				}
			}
			if err := os.WriteFile(out, data, 0o600); err != nil {
				return err
			}
			log(fmt.Sprintf("%s  invoice %d saved (%s)", o.ID, i+1, l.Label))
		}
		time.Sleep(Delay)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func printToPDF(ctx context.Context) ([]byte, error) {
	var pdf []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		buf, _, err := page.PrintToPDF().WithPrintBackground(true).Do(ctx)
		if err != nil {
			return err
		}
		pdf = buf
		return nil
	}))
	return pdf, err
}

// FetchInPage downloads a same-site URL through the page's own fetch,
// so the request carries the session exactly as the browser would send
// it. Returns the body and its content type.
func FetchInPage(ctx context.Context, u string) ([]byte, string, error) {
	js := `(async () => {
  try {
    const r = await fetch(` + jsString(u) + `, {credentials: "include"});
    if (!r.ok) return JSON.stringify({error: "HTTP " + r.status});
    const bytes = new Uint8Array(await r.arrayBuffer());
    let bin = "";
    for (let i = 0; i < bytes.length; i += 0x8000) {
      bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    }
    return JSON.stringify({type: r.headers.get("content-type") || "", b64: btoa(bin)});
  } catch (e) {
    return JSON.stringify({error: String(e)});
  }
})()`
	var raw string
	err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Error string `json:"error"`
		Type  string `json:"type"`
		B64   string `json:"b64"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, "", err
	}
	if res.Error != "" {
		return nil, "", fmt.Errorf("%s", res.Error)
	}
	data, err := base64.StdEncoding.DecodeString(res.B64)
	if err != nil {
		return nil, "", err
	}
	return data, res.Type, nil
}
