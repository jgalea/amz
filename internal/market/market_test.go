package market

import "testing"

func TestGetAndAliases(t *testing.T) {
	for in, want := range map[string]string{"es": "es", "UK": "co.uk", "amazon.co.uk": "co.uk", "www.amazon.de": "de", "com.be": "be", "us": "com"} {
		s, err := Get(in)
		if err != nil {
			t.Fatalf("Get(%q): %v", in, err)
		}
		if s.Code != want {
			t.Errorf("Get(%q) = %s, want %s", in, s.Code, want)
		}
	}
	if _, err := Get("xx"); err == nil {
		t.Error("Get(xx) should fail")
	}
}

func TestResolve(t *testing.T) {
	got, err := Resolve("")
	if err != nil || len(got) != len(Default) {
		t.Fatalf("Resolve(\"\") = %d storefronts, %v; want %d", len(got), err, len(Default))
	}
	got, err = Resolve("es, de,es,uk")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Code != "es" || got[1].Code != "de" || got[2].Code != "co.uk" {
		t.Errorf("Resolve dedup/order wrong: %+v", got)
	}
	eu, err := Resolve("eu")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range eu {
		if !s.EU {
			t.Errorf("%s is not EU", s.Code)
		}
	}
	all, _ := Resolve("all")
	if len(all) != len(table) {
		t.Errorf("all = %d, want %d", len(all), len(table))
	}
}

func TestGuarantee(t *testing.T) {
	cases := []struct {
		country, date string
		want          int
	}{
		{"PT", "2026-01-01", 3},
		{"PT", "2021-06-01", 2},
		{"ES", "2023-01-01", 3},
		{"DE", "2023-01-01", 2},
		{"IE", "", 6},
		{"gb", "", 6},
		{"SE", "", 3},
		{"US", "", 0},
		{"XX", "", 2},
	}
	for _, c := range cases {
		if got := Guarantee(c.country, c.date); got != c.want {
			t.Errorf("Guarantee(%s, %s) = %d, want %d", c.country, c.date, got, c.want)
		}
	}
}

func TestByCountry(t *testing.T) {
	if s, ok := ByCountry("pt"); !ok || s.Code != "es" {
		t.Errorf("PT should be served by es, got %+v %v", s, ok)
	}
	if _, ok := ByCountry("JP"); ok {
		t.Error("JP has no storefront here")
	}
}
