package fetch

import (
	"strings"
	"testing"
)

func TestGetImageErrorHidesURL(t *testing.T) {
	_, err := getImage("http://127.0.0.1:1/pricehistory.png?key=SECRETVALUE", "keepa")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("error leaks the URL: %v", err)
	}
}
