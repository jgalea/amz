package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// Amazon keeps most of its auth in long-lived cookies, but some of the
// session state is issued as session cookies that Chrome drops on exit.
// Persisting the whole jar ourselves keeps a login usable between runs.

func SaveCookies(ctx context.Context, path string) (int, error) {
	var jar []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		got, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		jar = got
		return nil
	}))
	if err != nil {
		return 0, err
	}
	if len(jar) == 0 {
		return 0, fmt.Errorf("no cookies to save")
	}
	raw, err := json.Marshal(jar)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return 0, err
	}
	return len(jar), nil
}

func LoadCookies(ctx context.Context, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var jar []*network.Cookie
	if err := json.Unmarshal(raw, &jar); err != nil {
		return 0, err
	}
	params := make([]*network.CookieParam, 0, len(jar))
	for _, c := range jar {
		p := &network.CookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			SameSite: c.SameSite,
		}
		if c.Expires > 0 {
			e := cdpTime(c.Expires)
			p.Expires = &e
		}
		params = append(params, p)
	}
	err = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return storage.SetCookies(params).Do(ctx)
	}))
	if err != nil {
		return 0, err
	}
	return len(params), nil
}

func cdpTime(seconds float64) cdp.TimeSinceEpoch {
	return cdp.TimeSinceEpoch(time.Unix(int64(seconds), 0))
}
