package price

import "testing"

func TestRank(t *testing.T) {
	qs := []AccountQuote{
		{Account: "home", Type: "personal", Market: "es", PriceEUR: 100},
		{Account: "work", Type: "business", Market: "es", PriceEUR: 100, ExVATEUR: 82.64},
		{Account: "dead", Type: "personal", Market: "de", Error: "signed out"},
	}
	got := Rank(qs, true)
	if got[0].Account != "work" || got[0].Effective != 82.64 || got[0].Note == "" {
		t.Errorf("reclaiming business should win: %+v", got[0])
	}
	if got[2].Account != "dead" {
		t.Errorf("unpriced quote must sort last: %+v", got)
	}
	got = Rank(qs, false)
	if got[0].Effective != 100 || got[1].Effective != 100 {
		t.Errorf("without reclaim both cost the shelf price: %+v", got[:2])
	}
	for _, q := range got {
		if q.Type == "business" && q.Note == "" {
			t.Error("business quote without reclaim should say so")
		}
	}
}

func TestCountryName(t *testing.T) {
	for cc, want := range map[string]string{"PT": "Portugal", "es": "Spain", "GB": "United Kingdom", "AT": "Austria", "ZZ": "ZZ"} {
		if got := CountryName(cc); got != want {
			t.Errorf("CountryName(%s) = %q, want %q", cc, got, want)
		}
	}
}
