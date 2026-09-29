// Package market is the table of Amazon storefronts amz knows about.
package market

import (
	"fmt"
	"strings"
)

// Storefront is one Amazon marketplace. Code is what the user types and
// what the rest of the program keys on; it doubles as the domain suffix.
type Storefront struct {
	Code           string
	Host           string
	Country        string
	CountryLocal   string
	CountryCode    string
	Currency       string
	AcceptLanguage string
	// ASINShared is true where the European catalogue is shared, so the
	// same ASIN is the same product; .com, .pl and .se assign their own.
	ASINShared bool
	EU         bool
	// Camel is the CamelCamelCamel region slug, or "" when Camel does
	// not track the storefront.
	Camel string
	// GuaranteeYears is the legal conformity guarantee for goods sold to
	// consumers in that country. Two years is the EU floor; some states
	// give more.
	GuaranteeYears int
}

func (s Storefront) Base() string { return "https://" + s.Host }

var table = []Storefront{
	{"es", "www.amazon.es", "Spain", "España", "ES", "EUR", "es-ES,es;q=0.9,en;q=0.8", true, true, "es", 3},
	{"de", "www.amazon.de", "Germany", "Deutschland", "DE", "EUR", "de-DE,de;q=0.9,en;q=0.8", true, true, "de", 2},
	{"fr", "www.amazon.fr", "France", "France", "FR", "EUR", "fr-FR,fr;q=0.9,en;q=0.8", true, true, "fr", 2},
	{"it", "www.amazon.it", "Italy", "Italia", "IT", "EUR", "it-IT,it;q=0.9,en;q=0.8", true, true, "it", 2},
	{"nl", "www.amazon.nl", "Netherlands", "Nederland", "NL", "EUR", "nl-NL,nl;q=0.9,en;q=0.8", true, true, "", 2},
	{"be", "www.amazon.com.be", "Belgium", "België", "BE", "EUR", "nl-BE,nl;q=0.9,fr;q=0.8", true, true, "", 2},
	{"ie", "www.amazon.ie", "Ireland", "Ireland", "IE", "EUR", "en-IE,en;q=0.9", true, true, "", 6},
	{"pl", "www.amazon.pl", "Poland", "Polska", "PL", "PLN", "pl-PL,pl;q=0.9,en;q=0.8", false, true, "", 2},
	{"se", "www.amazon.se", "Sweden", "Sverige", "SE", "SEK", "sv-SE,sv;q=0.9,en;q=0.8", false, true, "", 3},
	{"co.uk", "www.amazon.co.uk", "United Kingdom", "United Kingdom", "GB", "GBP", "en-GB,en;q=0.9", true, false, "uk", 6},
	{"com", "www.amazon.com", "United States", "United States", "US", "USD", "en-US,en;q=0.9", false, false, "us", 0},
}

// Default is the sweep for someone buying into the EU single market.
var Default = []string{"es", "de", "fr", "it", "nl", "co.uk"}

var aliases = map[string]string{
	"uk": "co.uk", "gb": "co.uk", "amazon.co.uk": "co.uk", "us": "com",
	"amazon.com.be": "be", "com.be": "be",
}

// Get looks a storefront up by code, domain suffix or a few aliases.
func Get(code string) (Storefront, error) {
	c := strings.ToLower(strings.TrimSpace(code))
	c = strings.TrimPrefix(c, "www.")
	c = strings.TrimPrefix(c, "amazon.")
	if a, ok := aliases[c]; ok {
		c = a
	}
	for _, s := range table {
		if s.Code == c {
			return s, nil
		}
	}
	return Storefront{}, fmt.Errorf("unknown storefront %q; known: %s", code, strings.Join(Codes(), ", "))
}

// Resolve turns a comma list ("es,de", "all", "eu", "") into storefronts.
// Empty means Default.
func Resolve(list string) ([]Storefront, error) {
	list = strings.TrimSpace(strings.ToLower(list))
	var codes []string
	switch list {
	case "":
		codes = Default
	case "all":
		codes = Codes()
	case "eu":
		for _, s := range table {
			if s.EU {
				codes = append(codes, s.Code)
			}
		}
	default:
		for _, c := range strings.Split(list, ",") {
			if c = strings.TrimSpace(c); c != "" {
				codes = append(codes, c)
			}
		}
	}
	var out []Storefront
	seen := map[string]bool{}
	for _, c := range codes {
		s, err := Get(c)
		if err != nil {
			return nil, err
		}
		if !seen[s.Code] {
			seen[s.Code] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func All() []Storefront { return append([]Storefront(nil), table...) }

func Codes() []string {
	var out []string
	for _, s := range table {
		out = append(out, s.Code)
	}
	return out
}

// ByCountry finds the storefront serving a two-letter country code, so an
// account's country can pick a default home store. Portugal has no
// storefront of its own and is served by amazon.es.
func ByCountry(cc string) (Storefront, bool) {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if cc == "PT" {
		cc = "ES"
	}
	for _, s := range table {
		if s.CountryCode == cc {
			return s, true
		}
	}
	return Storefront{}, false
}

// Guarantee is the legal guarantee in years for a consumer in the given
// country, for goods bought on or after the given ISO date. Countries
// without a storefront still have a guarantee: Portugal moved to three
// years on 2022-01-01 (Decreto-Lei 84/2021), as did Spain.
func Guarantee(country, dateISO string) int {
	cc := strings.ToUpper(strings.TrimSpace(country))
	switch cc {
	case "PT", "ES":
		if dateISO != "" && dateISO < "2022-01-01" {
			return 2
		}
		return 3
	case "SE":
		return 3
	case "IE", "GB", "UK":
		return 6
	case "US":
		return 0
	}
	if s, ok := ByCountry(cc); ok {
		return s.GuaranteeYears
	}
	return 2
}
