package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/jgalea/amz/internal/store"
)

func TestShouldNotify(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	hit := Outcome{Hit: true, Watch: store.Watch{ASIN: "B0TEST0001", Below: 100}, Lowest: 90, Market: "de"}
	if !ShouldNotify(hit, now) {
		t.Error("first hit must notify")
	}
	hit.Watch.NotifiedAt = now.Add(-2 * time.Hour).Format(time.RFC3339)
	if ShouldNotify(hit, now) {
		t.Error("a hit alerted two hours ago must stay quiet")
	}
	hit.Watch.NotifiedAt = now.Add(-30 * time.Hour).Format(time.RFC3339)
	if !ShouldNotify(hit, now) {
		t.Error("a hit alerted yesterday may alert again")
	}
	miss := hit
	miss.Hit = false
	if ShouldNotify(miss, now) {
		t.Error("no hit, no alert")
	}
}

func TestMessage(t *testing.T) {
	o := Outcome{Hit: true, Watch: store.Watch{ASIN: "B0TEST0001", Below: 100}, Lowest: 89.5, Market: "de", Used: true, Title: strings.Repeat("Long title ", 10)}
	m := Message(o)
	for _, want := range []string{"89.50 EUR (used)", "amazon.de", "below 100.00", "https://www.amazon.de/dp/B0TEST0001"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q: %s", want, m)
		}
	}
	if len(m) > 220 {
		t.Errorf("title not truncated: %d chars", len(m))
	}
}

func TestCheckRejectsUnknownMarket(t *testing.T) {
	o := Check(store.Watch{ASIN: "B0TEST0001", Markets: "xx", Below: 1}, "PT", false)
	if o.Error == "" || o.Hit {
		t.Errorf("bad market should error: %+v", o)
	}
}
