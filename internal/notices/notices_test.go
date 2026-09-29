package notices

import "testing"

func TestFromDir(t *testing.T) {
	got, scanned, err := FromDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 3 {
		t.Fatalf("scanned %d files, want 3", scanned)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 notice (protection), got %d: %+v", len(got), got)
	}
	n := got[0]
	if n.Kind != "protection" || n.Date != "2026-09-21" || n.Subject != "Protección de tu cuenta de Amazon" || n.MessageID != "0101019example000-protection@email.amazonses.com" {
		t.Errorf("notice: %+v", n)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		from, subject, snippet, want string
		ok                           bool
	}{
		{"account-update@amazon.es", "Your Amazon account has been restricted", "", "restriction", true},
		{"no-reply@amazon.co.uk", "Important information about your returns activity", "", "returns-warning", true},
		{"order-update@amazon.de", "Ihre Bestellung wurde storniert", "Wir haben Ihre Bestellung storniert, da", "cancelled-policy", true},
		{"order-update@amazon.es", "Your order has been cancelled", "as you requested", "", false},
		{"deals@amazon.es", "Ofertas de la semana", "", "", false},
		{"security@amazon-verify.example.net", "Your account has been restricted", "", "", false},
		{"marketplace-messages@amazon.com", "Refund request declined for order", "", "returns-warning", true},
	}
	for _, c := range cases {
		kind, ok := Classify(c.from, c.subject, c.snippet)
		if kind != c.want || ok != c.ok {
			t.Errorf("Classify(%q, %q) = %q,%v want %q,%v", c.from, c.subject, kind, ok, c.want, c.ok)
		}
	}
}
