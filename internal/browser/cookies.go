package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// Amazon keeps most of its auth in long-lived cookies, but some of the
// session state is issued as session cookies that Chrome drops on exit.
// Persisting the whole jar ourselves keeps a login usable between runs.
// Amazon also reissues its session cookies while you browse, and the
// browser is killed before it flushes them to the profile, so the jar is
// refreshed after every run; restoring the login-time copy instead hands
// Amazon tokens it has already replaced.

func liveCookies(ctx context.Context) ([]*network.Cookie, error) {
	var jar []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		got, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		jar = got
		return nil
	}))
	return jar, err
}

func SaveCookies(ctx context.Context, path string) (int, error) {
	jar, err := liveCookies(ctx)
	if err != nil {
		return 0, err
	}
	return writeJar(path, jar)
}

// UpdateCookies writes the browser's current cookies over the saved jar,
// keeping saved cookies the browser no longer holds. Only Amazon's own
// cookies are taken; product pages also collect ad-network ones.
func UpdateCookies(ctx context.Context, path string) (int, error) {
	all, err := liveCookies(ctx)
	if err != nil {
		return 0, err
	}
	live := all[:0]
	for _, c := range all {
		if strings.Contains(c.Domain, "amazon") {
			live = append(live, c)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var saved []*network.Cookie
	if err := json.Unmarshal(raw, &saved); err != nil {
		return 0, err
	}
	return writeJar(path, mergeJar(saved, live))
}

func mergeJar(saved, live []*network.Cookie) []*network.Cookie {
	key := func(c *network.Cookie) string { return c.Name + "\x00" + c.Domain + "\x00" + c.Path }
	fresh := make(map[string]*network.Cookie, len(live))
	for _, c := range live {
		fresh[key(c)] = c
	}
	out := make([]*network.Cookie, 0, len(saved)+len(live))
	for _, c := range saved {
		if l, ok := fresh[key(c)]; ok {
			out = append(out, l)
			delete(fresh, key(c))
			continue
		}
		out = append(out, c)
	}
	for _, c := range live {
		if _, ok := fresh[key(c)]; ok {
			out = append(out, c)
		}
	}
	return out
}

func writeJar(path string, jar []*network.Cookie) (int, error) {
	if len(jar) == 0 {
		return 0, fmt.Errorf("no cookies to save")
	}
	raw, err := json.Marshal(jar)
	if err != nil {
		return 0, err
	}
	if err := private.Dir(filepath.Dir(path)); err != nil {
		return 0, err
	}
	if err := private.WriteFile(path, raw); err != nil {
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
