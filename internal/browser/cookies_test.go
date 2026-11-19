package browser

import (
	"testing"

	"github.com/chromedp/cdproto/network"
)

func TestMergeJarTakesRefreshedCookies(t *testing.T) {
	saved := []*network.Cookie{
		{Name: "x-acbes", Domain: ".amazon.es", Path: "/", Value: "login"},
		{Name: "at-acbde", Domain: ".amazon.de", Path: "/", Value: "de-login"},
		{Name: "session-token", Domain: ".amazon.es", Path: "/", Value: "login"},
	}
	live := []*network.Cookie{
		{Name: "session-token", Domain: ".amazon.es", Path: "/", Value: "rotated"},
		{Name: "x-acbes", Domain: ".amazon.es", Path: "/", Value: "rotated"},
		{Name: "csm-hit", Domain: "www.amazon.es", Path: "/", Value: "new"},
	}
	got := mergeJar(saved, live)
	if len(got) != 4 {
		t.Fatalf("got %d cookies, want 4", len(got))
	}
	want := map[string]string{
		"x-acbes|.amazon.es":       "rotated",
		"session-token|.amazon.es": "rotated",
		"at-acbde|.amazon.de":      "de-login",
		"csm-hit|www.amazon.es":    "new",
	}
	for _, c := range got {
		k := c.Name + "|" + c.Domain
		if want[k] != c.Value {
			t.Errorf("%s = %q, want %q", k, c.Value, want[k])
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("missing %v", want)
	}
}

func TestMergeJarKeysOnPath(t *testing.T) {
	saved := []*network.Cookie{{Name: "a", Domain: ".amazon.es", Path: "/", Value: "root"}}
	live := []*network.Cookie{{Name: "a", Domain: ".amazon.es", Path: "/gp", Value: "gp"}}
	if got := mergeJar(saved, live); len(got) != 2 {
		t.Fatalf("got %d cookies, want 2 (different paths are different cookies)", len(got))
	}
}
