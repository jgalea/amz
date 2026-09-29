package amazon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Summary is what an order's printable summary says about its money:
// Amazon's own refund total, which covers refunds that went to gift
// card balance or were paid under another reference.
type Summary struct {
	OrderID     string
	Total       float64
	RefundTotal float64
	// OpenReturn is set while some item is still "Return started" or
	// approved but not yet refunded.
	OpenReturn bool
}

var (
	summaryTotalRe  = regexp.MustCompile(`(?:^|[^d] )Total:\s*([€$£]?\s*[\d.,]+\s*€?)`)
	summaryRefundRe = regexp.MustCompile(`Refund Total\s*([€$£]?\s*[\d.,]+\s*€?)`)
	summaryOpenRe   = regexp.MustCompile(`Return started|Return request approved|Devolución iniciada`)
	summaryFileRe   = regexp.MustCompile(`_(\d{3}-\d{7}-\d{7})_summary\.html$`)
)

func ParseSummary(doc *goquery.Document) Summary {
	doc.Find("script, style, noscript").Remove()
	text := Clean(doc.Find("body").Text())
	var s Summary
	if id := orderIDRe.FindString(text); id != "" {
		s.OrderID = id
	}
	if m := summaryTotalRe.FindStringSubmatch(text); m != nil {
		s.Total, _ = ParseAmount(m[1])
	}
	if m := summaryRefundRe.FindStringSubmatch(text); m != nil {
		s.RefundTotal, _ = ParseAmount(m[1])
	}
	s.OpenReturn = summaryOpenRe.MatchString(text)
	return s
}

// LoadSummaries reads every <date>_<id>_summary.html that `amz invoices`
// saved under dir, keyed by order id.
func LoadSummaries(dir string) (map[string]Summary, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*_summary.html"))
	if err != nil {
		return nil, err
	}
	out := map[string]Summary{}
	for _, p := range paths {
		m := summaryFileRe.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		f, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(f)))
		if err != nil {
			return nil, err
		}
		s := ParseSummary(doc)
		s.OrderID = m[1]
		out[m[1]] = s
	}
	return out, nil
}
