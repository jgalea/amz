package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccountRoundTrip(t *testing.T) {
	t.Setenv("AMZ_CONFIG_DIR", t.TempDir())
	a := Account{Name: "work", Type: Business, Email: "label", Country: "PT", Home: "es", Storefronts: []string{"de", "fr"}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != Business || got.Country != "PT" || got.Home != "es" || len(got.Storefronts) != 2 {
		t.Errorf("round trip lost fields: %+v", got)
	}
	mk, err := got.Markets()
	if err != nil {
		t.Fatal(err)
	}
	if len(mk) != 3 || mk[0].Code != "es" {
		t.Errorf("Markets() = %+v", mk)
	}
	list, err := Accounts()
	if err != nil || len(list) != 1 {
		t.Fatalf("Accounts() = %v, %v", list, err)
	}
	if _, err := Resolve(""); err != nil {
		t.Errorf("one account should resolve without a name: %v", err)
	}
	b := Account{Name: "home", Type: Personal, Home: "de"}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(""); err == nil {
		t.Error("two accounts and no name must be an error")
	}
	all, err := Selected("all")
	if err != nil || len(all) != 2 {
		t.Errorf("Selected(all) = %v, %v", all, err)
	}
	if err := Remove("home"); err != nil {
		t.Fatal(err)
	}
	if Remove("home") == nil {
		t.Error("removing twice should fail")
	}
}

func TestBadInput(t *testing.T) {
	t.Setenv("AMZ_CONFIG_DIR", t.TempDir())
	if err := (Account{Name: "../x", Type: Personal, Home: "es"}).Save(); err == nil {
		t.Error("path-like names must be rejected")
	}
	if err := (Account{Name: "ok", Type: "corporate", Home: "es"}).Save(); err == nil {
		t.Error("unknown type must be rejected")
	}
	if err := (Account{Name: "ok", Type: Personal, Home: "zz"}).Save(); err == nil {
		t.Error("unknown storefront must be rejected")
	}
}

// The amz layout, read without an account.json, must come back as a
// personal account on the storefront its market file names.
func TestLegacyMarketFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AMZ_CONFIG_DIR", dir)
	acc := filepath.Join(dir, "accounts", "old")
	if err := os.MkdirAll(acc, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(acc, "market"), []byte("co.uk\n"), 0o600)
	os.WriteFile(filepath.Join(acc, "cookies.json"), []byte("[]"), 0o600)
	a, err := Load("old")
	if err != nil {
		t.Fatal(err)
	}
	if a.Home != "co.uk" || a.Type != Personal || a.Country != "GB" {
		t.Errorf("legacy account = %+v", a)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(acc, "market")); !os.IsNotExist(err) {
		t.Error("market file should be gone after Save")
	}
	if _, err := os.Stat(filepath.Join(acc, "cookies.json")); err != nil {
		t.Error("cookie jar must survive")
	}
}

func TestSettings(t *testing.T) {
	t.Setenv("AMZ_CONFIG_DIR", t.TempDir())
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Notify.MacOS {
		t.Error("macOS notifications default on")
	}
	s.Notify.Ntfy.Topic = "t"
	s.Mail.Host = "imap.example.com"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil || got.Notify.Ntfy.Topic != "t" || got.Mail.Host != "imap.example.com" {
		t.Errorf("settings round trip: %+v %v", got, err)
	}
}
