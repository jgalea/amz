// Package fetch reads public Amazon pages anonymously, politely: one
// request every couple of seconds per host, cached for a few hours.
// The dedicated review endpoints sit behind a sign-in wall and are
// never requested.
package fetch

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jgalea/amz/internal/market"
)

// The Chrome token carries only a major version on purpose: a full
// four-part version reads as an IP address to secret scanners.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126 Safari/537.36"

var (
	// CacheDir holds fetched pages; empty disables the cache.
	CacheDir string
	CacheTTL = 6 * time.Hour
	// MinDelay is the pause between two requests to the same host.
	MinDelay = 2 * time.Second

	lastHit   = map[string]time.Time{}
	lastHitMu sync.Mutex
	client    = &http.Client{Timeout: 30 * time.Second}
)

var (
	ErrBlocked  = errors.New("bot check or sign-in wall served instead of the page")
	ErrNotFound = errors.New("not listed on this storefront")
)

type Page struct {
	URL       string
	HTML      string
	FromCache bool
}

func headers(req *http.Request, sf market.Storefront) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", sf.AcceptLanguage)
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Device-Memory", "8")
}

func cachePath(u string) string {
	sum := sha256.Sum256([]byte(u))
	return filepath.Join(CacheDir, hex.EncodeToString(sum[:12])+".json")
}

type cached struct {
	At   int64  `json:"at"`
	URL  string `json:"url"`
	HTML string `json:"html"`
}

func readCache(u string) (string, bool) {
	if CacheDir == "" {
		return "", false
	}
	raw, err := os.ReadFile(cachePath(u))
	if err != nil {
		return "", false
	}
	var c cached
	if json.Unmarshal(raw, &c) != nil || time.Since(time.Unix(c.At, 0)) > CacheTTL {
		return "", false
	}
	return c.HTML, true
}

func writeCache(u, html string) {
	if CacheDir == "" {
		return
	}
	if err := os.MkdirAll(CacheDir, 0o700); err != nil {
		return
	}
	raw, _ := json.Marshal(cached{At: time.Now().Unix(), URL: u, HTML: html})
	os.WriteFile(cachePath(u), raw, 0o600)
}

func throttle(host string) {
	lastHitMu.Lock()
	last, ok := lastHit[host]
	var wait time.Duration
	if ok {
		wait = MinDelay - time.Since(last) + time.Duration(rand.Int63n(int64(600*time.Millisecond)))
	}
	lastHit[host] = time.Now().Add(wait)
	lastHitMu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}

// Blocked reports whether the HTML is a captcha or sign-in page rather
// than the product page asked for.
func Blocked(html string) bool {
	head := html
	if len(head) > 200_000 {
		head = head[:200_000]
	}
	if strings.Contains(head, "productTitle") {
		return false
	}
	// A real Amazon page is hundreds of kilobytes; a few kilobytes is an
	// interstitial or an error shell.
	if len(head) < 20_000 {
		return true
	}
	for _, m := range []string{"ap_signin", "/ax/claim", "Enter the characters you see below", "Type the characters you see in this image", "api-services-support@amazon.com", "validateCaptcha", "bm-verify", "triggerInterstitialChallenge"} {
		if strings.Contains(head, m) {
			return true
		}
	}
	return false
}

func DetailURL(asin string, sf market.Storefront) string {
	return sf.Base() + "/dp/" + strings.ToUpper(asin)
}

// Get fetches any public URL on a storefront with the same caching and
// pacing as a detail page.
func Get(u string, sf market.Storefront, refresh bool) (Page, error) {
	if !refresh {
		if html, ok := readCache(u); ok {
			return Page{URL: u, HTML: html, FromCache: true}, nil
		}
	}
	throttle(sf.Host)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return Page{}, err
	}
	headers(req, sf)
	resp, err := client.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("%s: %w", sf.Code, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return Page{}, fmt.Errorf("%s: %w", sf.Code, ErrNotFound)
	}
	if resp.StatusCode >= 400 {
		return Page{}, fmt.Errorf("%s: HTTP %d for %s", sf.Code, resp.StatusCode, u)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return Page{}, err
	}
	html := string(body)
	if Blocked(html) {
		return Page{}, fmt.Errorf("%s: %w", sf.Code, ErrBlocked)
	}
	writeCache(u, html)
	return Page{URL: resp.Request.URL.String(), HTML: html}, nil
}

// Detail fetches a product detail page.
func Detail(asin string, sf market.Storefront, refresh bool) (Page, error) {
	return Get(DetailURL(asin, sf), sf, refresh)
}

// ChartURL is the public CamelCamelCamel price-history chart for an
// ASIN. Camel's HTML pages sit behind a Cloudflare challenge, but the
// chart renderer is open, and with legend=1 the image carries the exact
// lowest, highest and current price with dates, so the PNG is the data.
// period is one of all, 1y, 6m, 3m, 1m.
func ChartURL(asin string, sf market.Storefront, source, period string) (string, error) {
	if sf.Camel == "" {
		return "", fmt.Errorf("CamelCamelCamel does not cover amazon.%s", sf.Code)
	}
	if source == "" {
		source = "amazon"
	}
	if period == "" {
		period = "all"
	}
	return fmt.Sprintf("https://charts.camelcamelcamel.com/%s/%s/%s.png?force=1&zero=0&w=1710&h=1026&desired=false&legend=1&ilt=1&tp=%s&fo=0&lang=en", sf.Camel, strings.ToUpper(asin), source, period), nil
}

// minChartDensity separates a plotted chart from Camel's near-white
// "no data" placeholder: a chart with gridlines, a series and a legend
// never compresses below this many bytes per pixel, the placeholder
// lands at about a third of it.
const minChartDensity = 0.02

// PlottedChart reports whether a PNG is a real chart rather than the
// placeholder.
func PlottedChart(content []byte) bool {
	if len(content) < 4_000 || !strings.HasPrefix(string(content[:8]), "\x89PNG") || len(content) < 24 {
		return false
	}
	w := binary.BigEndian.Uint32(content[16:20])
	h := binary.BigEndian.Uint32(content[20:24])
	if w == 0 || h == 0 {
		return false
	}
	return float64(len(content))/float64(w*h) >= minChartDensity
}

// Chart downloads a price-history chart to dest. It returns false, nil
// when Camel has no data for the ASIN.
func Chart(asin string, sf market.Storefront, dest, period string) (bool, error) {
	u, err := ChartURL(asin, sf, "amazon", period)
	if err != nil {
		return false, err
	}
	throttle("charts.camelcamelcamel.com")
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image") {
		return false, fmt.Errorf("camel: HTTP %d, %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return false, err
	}
	if !PlottedChart(body) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dest, body, 0o644)
}
