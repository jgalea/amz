// Package fx converts currencies with the ECB daily reference rates:
// free, no key, published once per working day, mid-market. Fair for a
// comparison, not what a card issuer charges.
package fx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const ecbURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

var (
	// CacheDir is where the rate table is kept between runs.
	CacheDir string
	ttl      = 12 * time.Hour
	rateRe   = regexp.MustCompile(`currency='(\w{3})'\s+rate='([\d.]+)'`)
)

// Table is EUR-based: {"EUR": 1, "GBP": 0.85, ...}.
type Table map[string]float64

type cached struct {
	At    int64 `json:"at"`
	Rates Table `json:"rates"`
}

func cachePath() string { return filepath.Join(CacheDir, "ecb.json") }

// Rates returns the current table, from cache when fresh enough.
func Rates() (Table, error) {
	if CacheDir != "" {
		if raw, err := os.ReadFile(cachePath()); err == nil {
			var c cached
			if json.Unmarshal(raw, &c) == nil && time.Since(time.Unix(c.At, 0)) < ttl && len(c.Rates) > 0 {
				return c.Rates, nil
			}
		}
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(ecbURL)
	if err != nil {
		return nil, fmt.Errorf("fetching ECB reference rates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetching ECB reference rates: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	t := Parse(string(body))
	if len(t) < 2 {
		return nil, fmt.Errorf("ECB response parsed to no rates; the XML layout may have changed")
	}
	if CacheDir != "" {
		if err := os.MkdirAll(CacheDir, 0o700); err == nil {
			raw, _ := json.Marshal(cached{At: time.Now().Unix(), Rates: t})
			os.WriteFile(cachePath(), raw, 0o600)
		}
	}
	return t, nil
}

// Parse reads the ECB XML into a table. EUR is always 1.
func Parse(xml string) Table {
	t := Table{"EUR": 1}
	for _, m := range rateRe.FindAllStringSubmatch(xml, -1) {
		if v, err := strconv.ParseFloat(m[2], 64); err == nil {
			t[m[1]] = v
		}
	}
	return t
}

// Convert turns amount in from into to; ok is false when either
// currency is missing from the table.
func (t Table) Convert(amount float64, from, to string) (float64, bool) {
	f, okf := t[from]
	g, okt := t[to]
	if !okf || !okt || f == 0 {
		return 0, false
	}
	return amount / f * g, true
}
