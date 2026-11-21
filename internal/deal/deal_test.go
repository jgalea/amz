package deal

import "testing"

const legend = `FLEXISPOT Escritorio Elevable
€440
Price type Lowest Highest Current
I 3rd party new €319,98 (Apr 28, 2026) €419,99 (May 12, 2026) €419,99 (Nov 21, 2026)
Amazon €1.309,00 (Jan 3, 2026) €1.499,00 (Mar 9, 2026) €1.399,50 (Nov 21, 2026)
© camelcamelcamel.com - https://3cmls.co/ES/B09KNFL1T2`

func TestParseLegend(t *testing.T) {
	rows, why := ParseLegend(legend)
	if len(rows) != 2 || why != "" {
		t.Fatalf("rows = %v, why = %q", rows, why)
	}
	n := rows[0]
	if n.Series != "new" || n.Currency != "EUR" || n.Lowest.Price != 319.98 || n.Lowest.Date != "2026-04-28" || n.Highest.Price != 419.99 || n.Current.Date != "2026-11-21" {
		t.Errorf("new row = %+v", n)
	}
	a := rows[1]
	if a.Series != "amazon" || a.Lowest.Price != 1309 || a.Current.Price != 1399.5 {
		t.Errorf("amazon row = %+v", a)
	}
}

func TestParseLegendNoData(t *testing.T) {
	for _, text := range []string{"", "No price history is available for this product\n© camelcamelcamel.com", "3rd party new €319,98 (Apr 28, 2026) €419,99 (May 12, 2026)"} {
		if rows, why := ParseLegend(text); rows != nil || why == "" {
			t.Errorf("%q: rows = %v, why = %q", text, rows, why)
		}
	}
}

func TestParseLegendRejectsInconsistentRow(t *testing.T) {
	if rows, _ := ParseLegend("3rd party new €519,98 (Apr 28, 2026) €419,99 (May 12, 2026) €419,99 (Nov 21, 2026)"); rows != nil {
		t.Errorf("lowest above highest was accepted: %v", rows)
	}
}

func priced(market string, eur float64, comparable bool) Store {
	return Store{Market: market, PriceEUR: &eur, Comparable: comparable}
}

func TestDeriveAndDecide(t *testing.T) {
	es := priced("es", 325.79, true)
	es.Low30dEUR = ptr(342.94)
	es.Camel = []Row{{Series: "new", Currency: "EUR", Lowest: Point{319.98, "2026-04-28"}, Highest: Point{419.99, "2026-05-12"}}}
	Derive(&es)
	if *es.VsLow30dPct != -5 || *es.VsLow1yPct != 1.8 || !*es.At1yLow {
		t.Errorf("es signals: %v %v %v", *es.VsLow30dPct, *es.VsLow1yPct, *es.At1yLow)
	}
	de := priced("de", 250, false)
	nothing := Store{Market: "it"}
	r := Report{Stores: []Store{es, de, nothing}}
	Decide(&r)
	if r.Verdict.CheapestStore == nil || *r.Verdict.CheapestStore != "es" {
		t.Fatalf("cheapest = %v", r.Verdict.CheapestStore)
	}
	if r.Verdict.CurrentVs1yLowPct == nil {
		t.Error("1y signal not carried to the verdict")
	}
}

func TestUnevaluatedSignalsStayNil(t *testing.T) {
	s := priced("es", 100, true)
	Derive(&s)
	if s.VsLow30dPct != nil || s.VsLow1yPct != nil || s.At1yLow != nil || s.Low1yEUR != nil {
		t.Error("a signal with no input was filled in")
	}
	r := Report{Stores: []Store{s, {Market: "de"}}}
	Decide(&r)
	if r.Verdict.CurrentVs30dLowPct != nil || r.Verdict.At1yLow != nil {
		t.Error("verdict signals should be nil without history")
	}
	empty := Report{Stores: []Store{{Market: "es"}}}
	Decide(&empty)
	if empty.Verdict.CheapestStore != nil || empty.Verdict.CheapestReason == "" {
		t.Error("an empty report named a cheapest store")
	}
	nope := Report{Stores: []Store{priced("de", 90, false)}}
	Decide(&nope)
	if nope.Verdict.CheapestStore != nil {
		t.Error("a store priced for another destination was chosen")
	}
}
