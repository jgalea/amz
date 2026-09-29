package fx

import "testing"

func TestParseAndConvert(t *testing.T) {
	xml := `<Cube currency='USD' rate='1.0850'/><Cube currency='GBP' rate='0.8400'/><Cube currency='PLN' rate='4.3'/>`
	tb := Parse(xml)
	if len(tb) != 4 || tb["EUR"] != 1 || tb["GBP"] != 0.84 {
		t.Fatalf("table = %v", tb)
	}
	eur, ok := tb.Convert(84, "GBP", "EUR")
	if !ok || eur < 99.99 || eur > 100.01 {
		t.Errorf("84 GBP = %v EUR, %v", eur, ok)
	}
	if _, ok := tb.Convert(1, "SEK", "EUR"); ok {
		t.Error("missing currency must not convert")
	}
	if len(Parse("<nothing/>")) != 1 {
		t.Error("empty XML should give EUR only")
	}
}
