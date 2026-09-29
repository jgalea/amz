// Package config holds amz's on-disk state: the accounts (each with
// its own Chrome profile and cookie jar) and the settings file.
package config

import (
	"encoding/json"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/jgalea/amz/internal/market"
)

const (
	Personal = "personal"
	Business = "business"
)

// Account is one Amazon login. One login spans several storefronts (an
// EU account signs in to .es, .de, .fr and the rest), each of which keeps
// its own order history, so Storefronts lists the ones worth syncing.
type Account struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Email is a label for the person reading `accounts list`; nothing
	// is ever sent to it.
	Email string `json:"email,omitempty"`
	// Country is where orders are delivered, as a two-letter code. It
	// drives the legal guarantee table and which price is "yours".
	Country     string   `json:"country,omitempty"`
	Home        string   `json:"home"`
	Storefronts []string `json:"storefronts"`
	CreatedAt   string   `json:"created_at,omitempty"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Dir() string {
	if d := os.Getenv("AMZ_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".amz"
	}
	return filepath.Join(home, ".amz")
}

func accountsRoot() string           { return filepath.Join(Dir(), "accounts") }
func DBPath() string                 { return filepath.Join(Dir(), "amz.db") }
func CachePath() string              { return filepath.Join(Dir(), "cache") }
func (a Account) Dir() string        { return filepath.Join(accountsRoot(), a.Name) }
func (a Account) ProfileDir() string { return filepath.Join(a.Dir(), "profile") }
func (a Account) CookiePath() string { return filepath.Join(a.Dir(), "cookies.json") }
func (a Account) path() string       { return filepath.Join(a.Dir(), "account.json") }

// Markets returns the account's storefronts as a table lookup, home
// first.
func (a Account) Markets() ([]market.Storefront, error) {
	codes := append([]string{a.Home}, a.Storefronts...)
	seen := map[string]bool{}
	var out []market.Storefront
	for _, c := range codes {
		if c == "" || seen[c] {
			continue
		}
		s, err := market.Get(c)
		if err != nil {
			return nil, fmt.Errorf("account %s: %w", a.Name, err)
		}
		seen[c] = true
		out = append(out, s)
	}
	return out, nil
}

func (a Account) HasStorefront(code string) bool {
	if a.Home == code {
		return true
	}
	for _, c := range a.Storefronts {
		if c == code {
			return true
		}
	}
	return false
}

func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("account name %q: use letters, digits, dot, dash or underscore", name)
	}
	return nil
}

// Load reads one account. A directory with a cookie jar but no
// account.json (the amz layout) is read as a personal account whose home
// is whatever the old `market` file says.
func Load(name string) (Account, error) {
	if err := ValidName(name); err != nil {
		return Account{}, err
	}
	a := Account{Name: name, Type: Personal, Home: "es"}
	raw, err := os.ReadFile(a.path())
	if os.IsNotExist(err) {
		if m, err := os.ReadFile(filepath.Join(a.Dir(), "market")); err == nil {
			if s, err := market.Get(strings.TrimSpace(string(m))); err == nil {
				a.Home = s.Code
			}
		}
		if a.Country == "" {
			if s, err := market.Get(a.Home); err == nil {
				a.Country = s.CountryCode
			}
		}
		return a, nil
	}
	if err != nil {
		return Account{}, err
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Account{}, fmt.Errorf("%s: %w", a.path(), err)
	}
	a.Name = name
	if a.Type == "" {
		a.Type = Personal
	}
	return a, nil
}

func (a Account) Save() error {
	if err := ValidName(a.Name); err != nil {
		return err
	}
	if a.Type != Personal && a.Type != Business {
		return fmt.Errorf("account type must be %s or %s", Personal, Business)
	}
	if _, err := market.Get(a.Home); err != nil {
		return err
	}
	for _, c := range a.Storefronts {
		if _, err := market.Get(c); err != nil {
			return err
		}
	}
	if err := private.Dir(a.Dir()); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := private.WriteFile(a.path(), append(raw, '\n')); err != nil {
		return err
	}
	// The amz-era marker is redundant once account.json exists.
	os.Remove(filepath.Join(a.Dir(), "market"))
	return nil
}

// Exists reports whether the account has anything on disk.
func (a Account) Exists() bool {
	_, err := os.Stat(a.Dir())
	return err == nil
}

func Remove(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	a := Account{Name: name}
	if !a.Exists() {
		return fmt.Errorf("no account %q", name)
	}
	return os.RemoveAll(a.Dir())
}

// Accounts lists the accounts on disk, by name.
func Accounts() ([]Account, error) {
	if err := Migrate(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(accountsRoot())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, e := range entries {
		if !e.IsDir() || !nameRe.MatchString(e.Name()) {
			continue
		}
		a, err := Load(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve picks the account to use: the one named, else the only one on
// disk, else "default". Several accounts and no name is an error, since
// guessing would silently read the wrong person's orders. "all" is
// passed through for the caller to expand.
func Resolve(name string) (Account, error) {
	if name != "" {
		if err := Migrate(); err != nil {
			return Account{}, err
		}
		return Load(name)
	}
	list, err := Accounts()
	if err != nil {
		return Account{}, err
	}
	switch len(list) {
	case 0:
		return Load("default")
	case 1:
		return list[0], nil
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	return Account{}, fmt.Errorf("several accounts exist (%s); pick one with --account, or --account all", strings.Join(names, ", "))
}

// Selected expands an --account value: a name, "all", or empty (the
// usual resolution rules).
func Selected(name string) ([]Account, error) {
	if name == "all" {
		list, err := Accounts()
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("no accounts yet (run `amz accounts add NAME`)")
		}
		return list, nil
	}
	a, err := Resolve(name)
	if err != nil {
		return nil, err
	}
	return []Account{a}, nil
}

// Migrate brings older layouts up to date, in place. The original
// single-account layout (~/.amz/{profile,cookies.json,market}) is moved
// under accounts/default, and every account directory without an
// account.json gets one written from its `market` file. Directories are
// renamed, never copied, so a signed-in session and the jar's mode
// survive; nothing leaves the config directory.
func Migrate() error {
	root := Dir()
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	for _, name := range []string{"profile", "cookies.json", "market"} {
		from := filepath.Join(root, name)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		to := filepath.Join(accountsRoot(), "default", name)
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("both %s and %s exist; move one aside by hand", from, to)
		}
		if err := private.Dir(filepath.Dir(to)); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("moving %s into the default account: %w", from, err)
		}
	}
	entries, err := os.ReadDir(accountsRoot())
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || !nameRe.MatchString(e.Name()) {
			continue
		}
		a, err := Load(e.Name())
		if err != nil {
			return err
		}
		if _, err := os.Stat(a.path()); err == nil {
			continue
		}
		a.CreatedAt = time.Now().Format(time.RFC3339)
		if err := a.Save(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s for the existing account %s (%s, home amazon.%s)\n", a.path(), a.Name, a.Type, a.Home)
	}
	return nil
}

// JarStatus is what the saved cookie jar says about an account, read
// without starting a browser. SignedIn means an unexpired Amazon auth
// token (an "at-*" cookie) is in the jar; Amazon can still have revoked
// it server-side, so this is a strong hint, not a guarantee.
type JarStatus struct {
	Exists   bool
	SignedIn bool
	Cookies  int
	SavedAt  time.Time
	Expires  time.Time
}

func (a Account) Jar() (JarStatus, error) {
	var st JarStatus
	info, err := os.Stat(a.CookiePath())
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Exists = true
	st.SavedAt = info.ModTime()
	raw, err := os.ReadFile(a.CookiePath())
	if err != nil {
		return st, err
	}
	var jar []*network.Cookie
	if err := json.Unmarshal(raw, &jar); err != nil {
		return st, fmt.Errorf("%s: %w", a.CookiePath(), err)
	}
	st.Cookies = len(jar)
	now := time.Now()
	for _, c := range jar {
		if !strings.HasPrefix(c.Name, "at-") {
			continue
		}
		exp := time.Unix(int64(c.Expires), 0)
		if c.Expires <= 0 || exp.After(now) {
			st.SignedIn = true
			if c.Expires > 0 && (st.Expires.IsZero() || exp.After(st.Expires)) {
				st.Expires = exp
			}
		}
	}
	return st, nil
}
