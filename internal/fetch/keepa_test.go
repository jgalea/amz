package fetch

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jgalea/amz/internal/market"
)

func pngHeader(w, h uint32) []byte {
	b := make([]byte, 64)
	copy(b, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	return b
}

func TestKeepaPlotted(t *testing.T) {
	if !keepaPlotted(pngHeader(keepaWidth, keepaHeight)) {
		t.Error("graph at the requested size rejected")
	}
	if keepaPlotted(pngHeader(500, 200)) {
		t.Error("no-history card accepted")
	}
	if keepaPlotted([]byte("<html>")) {
		t.Error("non-PNG accepted")
	}
}

func TestKeepaChartURL(t *testing.T) {
	t.Setenv("KEEPA_KEY", "")
	es, _ := market.Get("es")
	u, err := KeepaChartURL("b09knfl1t2", es, "new", "1y")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"asin=B09KNFL1T2", "domain=9", "range=365", "new=1", "amazon=0", "used=0"} {
		if !strings.Contains(u, want) {
			t.Errorf("url %q lacks %s", u, want)
		}
	}
	nl, _ := market.Get("nl")
	if _, err := KeepaChartURL("B09KNFL1T2", nl, "", ""); err == nil {
		t.Error("nl is not on Keepa")
	}
	if _, err := KeepaChartURL("B09KNFL1T2", es, "warehouse", ""); err == nil {
		t.Error("unknown price type accepted")
	}
}
