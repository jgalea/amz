package fetch

import (
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jgalea/amz/internal/market"
)

// keepaDomains maps storefront codes to Keepa's domain ids. Keepa does
// not track the other storefronts.
var keepaDomains = map[string]int{"com": 1, "co.uk": 2, "de": 3, "fr": 4, "it": 8, "es": 9}

var keepaRange = map[string]int{"1m": 31, "3m": 90, "6m": 180, "1y": 365, "all": 3650}

const keepaWidth, keepaHeight = 1600, 600

// KeepaChartURL is Keepa's price-history graph for an ASIN. Unlike
// Camel's it carries no lowest/highest legend, only the current price,
// but it is updated far more often and covers deal prices Camel misses.
// KEEPA_KEY, when set, is sent as the access key.
func KeepaChartURL(asin string, sf market.Storefront, prices, period string) (string, error) {
	domain, ok := keepaDomains[sf.Code]
	if !ok {
		return "", fmt.Errorf("Keepa does not cover amazon.%s", sf.Code)
	}
	if period == "" {
		period = "1y"
	}
	days, ok := keepaRange[period]
	if !ok {
		return "", fmt.Errorf("period must be all, 1y, 6m, 3m or 1m, not %q", period)
	}
	if prices == "" {
		prices = "amazon,new"
	}
	q := url.Values{}
	q.Set("asin", strings.ToUpper(asin))
	q.Set("domain", fmt.Sprint(domain))
	q.Set("range", fmt.Sprint(days))
	q.Set("width", fmt.Sprint(keepaWidth))
	q.Set("height", fmt.Sprint(keepaHeight))
	for _, t := range []string{"amazon", "new", "used"} {
		q.Set(t, "0")
	}
	for _, p := range strings.Split(prices, ",") {
		switch p = strings.TrimSpace(p); p {
		case "amazon", "new", "used":
			q.Set(p, "1")
		default:
			return "", fmt.Errorf("prices must be amazon, new or used, not %q", p)
		}
	}
	if k := os.Getenv("KEEPA_KEY"); k != "" {
		q.Set("key", k)
	}
	return "https://graph.keepa.com/pricehistory.png?" + q.Encode(), nil
}

// keepaPlotted reports whether a PNG is the requested graph. With no
// data Keepa answers with a fixed 500x200 "no price history" card
// whatever size was asked for.
func keepaPlotted(content []byte) bool {
	if len(content) < 24 || !strings.HasPrefix(string(content[:8]), "\x89PNG") {
		return false
	}
	return binary.BigEndian.Uint32(content[16:20]) == keepaWidth && binary.BigEndian.Uint32(content[20:24]) == keepaHeight
}

// KeepaChart downloads Keepa's graph to dest. It returns false, nil when
// Keepa has no history for the ASIN.
func KeepaChart(asin string, sf market.Storefront, dest, prices, period string) (bool, error) {
	u, err := KeepaChartURL(asin, sf, prices, period)
	if err != nil {
		return false, err
	}
	body, err := getImage(u, "keepa")
	if err != nil || !keepaPlotted(body) {
		return false, err
	}
	return true, writeFile(dest, body)
}
