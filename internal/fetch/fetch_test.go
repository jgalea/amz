package fetch

import (
	"os"
	"strings"
	"testing"

	"github.com/jgalea/amz/internal/market"
)

func TestPlottedChart(t *testing.T) {
	real, err := os.ReadFile("testdata/camel_chart_real.png")
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := os.ReadFile("testdata/camel_chart_placeholder.png")
	if err != nil {
		t.Fatal(err)
	}
	if !PlottedChart(real) {
		t.Error("real chart rejected")
	}
	if PlottedChart(placeholder) {
		t.Error("placeholder accepted")
	}
	if PlottedChart([]byte("<html>not a png</html>")) {
		t.Error("non-PNG accepted")
	}
}

func TestChartURL(t *testing.T) {
	es, _ := market.Get("es")
	u, err := ChartURL("b09b94956p", es, "", "1y")
	if err != nil || u != "https://charts.camelcamelcamel.com/es/B09B94956P/amazon.png?force=1&zero=0&w=1710&h=1026&desired=false&legend=1&ilt=1&tp=1y&fo=0&lang=en" {
		t.Errorf("url = %q, %v", u, err)
	}
	nl, _ := market.Get("nl")
	if _, err := ChartURL("B09B94956P", nl, "", ""); err == nil {
		t.Error("nl has no Camel region")
	}
}

func TestBlocked(t *testing.T) {
	if !Blocked(`<html><form action="/errors/validateCaptcha">Type the characters you see in this image</form></html>`) {
		t.Error("captcha page not detected")
	}
	big := `<html><span id="productTitle">Thing</span> ap_signin link in footer` + strings.Repeat("<p>filler</p>", 3000) + `</html>`
	if Blocked(big) {
		t.Error("product page with a sign-in link flagged")
	}
	if !Blocked(`<html>var i = 1; triggerInterstitialChallenge()</html>`) {
		t.Error("Akamai interstitial not detected")
	}
}

func TestChartPeriodAllowlist(t *testing.T) {
	sf, err := market.Get("es")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChartURL("B0TEST0001", sf, "amazon", "1y"); err != nil {
		t.Errorf("1y rejected: %v", err)
	}
	for _, bad := range []string{"../../x", "all/../../evil", "1y&tp=all"} {
		if _, err := ChartURL("B0TEST0001", sf, "amazon", bad); err == nil {
			t.Errorf("period %q accepted", bad)
		}
	}
}
