package reviews

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jgalea/amz/internal/product"
)

func review(id, date, body string, verified bool) product.Review {
	return product.Review{ID: id, Date: date, Body: body, BodyChars: len(body), Verified: verified, ProfileID: "amzn1.account." + id}
}

func signal(a Authenticity, key string) Signal {
	for _, s := range a.Signals {
		if s.Key == key {
			return s
		}
	}
	return Signal{}
}

func bodies(n int) []product.Review {
	var out []product.Review
	for i := 0; i < n; i++ {
		out = append(out, review(fmt.Sprintf("R%d", i), fmt.Sprintf("2024-%02d-10", i+1),
			fmt.Sprintf("Review number %d talks about a completely different aspect of the product, its %d-th feature, at some length so it counts.", i, i*7), true))
	}
	return out
}

func TestEmptyInputIsUnknownNotClean(t *testing.T) {
	a := Analyse(product.Product{}, nil)
	if a.Scored != 0 || len(a.Notes) == 0 {
		t.Errorf("empty input: %+v", a)
	}
}

func TestDateBurst(t *testing.T) {
	var rs []product.Review
	for i := 0; i < 8; i++ {
		rs = append(rs, review(fmt.Sprintf("R%d", i), fmt.Sprintf("2024-05-%02d", 10+i%3), "body text that is long enough "+strings.Repeat("x ", 20+i), true))
	}
	if s := signal(Analyse(product.Product{}, rs), "date_burst"); !s.Suspicious || !s.Evaluated {
		t.Errorf("burst not flagged: %+v", s)
	}
	if s := signal(Analyse(product.Product{}, bodies(8)), "date_burst"); s.Suspicious {
		t.Errorf("spread dates flagged: %+v", s)
	}
}

func TestVerifiedShare(t *testing.T) {
	rs := bodies(6)
	for i := range rs {
		rs[i].Verified = i < 2
	}
	if s := signal(Analyse(product.Product{}, rs), "verified_ratio"); !s.Suspicious {
		t.Errorf("low verified share not flagged: %+v", s)
	}
	if s := signal(Analyse(product.Product{}, bodies(6)), "verified_ratio"); s.Suspicious {
		t.Errorf("full verified share flagged: %+v", s)
	}
}

func TestDuplicates(t *testing.T) {
	rs := bodies(4)
	rs[1].Body = rs[0].Body + " Also fine."
	if s := signal(Analyse(product.Product{}, rs), "duplicate_pairs"); !s.Suspicious || s.Value != 1 {
		t.Errorf("duplicate not flagged: %+v", s)
	}
	if s := signal(Analyse(product.Product{}, bodies(4)), "duplicate_pairs"); s.Suspicious {
		t.Errorf("distinct bodies flagged: %+v", s)
	}
}

func TestDistributionShape(t *testing.T) {
	p := product.Product{Histogram: map[int]int{5: 70, 4: 5, 3: 8, 2: 5, 1: 12}}
	if s := signal(Analyse(p, bodies(4)), "distribution_shape"); !s.Suspicious {
		t.Errorf("bimodal not flagged: %+v", s)
	}
	p = product.Product{Histogram: map[int]int{5: 70, 4: 15, 3: 8, 2: 4, 1: 3}}
	if s := signal(Analyse(p, bodies(4)), "distribution_shape"); s.Suspicious {
		t.Errorf("normal taper flagged: %+v", s)
	}
	if s := signal(Analyse(product.Product{}, bodies(4)), "five_star_share"); s.Evaluated {
		t.Errorf("missing histogram must be unevaluated: %+v", s)
	}
}

func TestRepeatReviewerAndShortBodies(t *testing.T) {
	rs := bodies(4)
	rs[2].ProfileID = rs[0].ProfileID
	if s := signal(Analyse(product.Product{}, rs), "repeat_reviewers"); !s.Suspicious {
		t.Errorf("repeat reviewer not flagged: %+v", s)
	}
	rs = bodies(5)
	for i := range rs {
		rs[i].Body, rs[i].BodyChars = "Good.", 5
	}
	if s := signal(Analyse(product.Product{}, rs), "short_body_share"); !s.Suspicious {
		t.Errorf("short bodies not flagged: %+v", s)
	}
}

func TestTextFeatures(t *testing.T) {
	machine := review("m", "", "Overall, this product is a game-changer for my daily routine. It arrived well packaged and works perfectly. The build quality is excellent and the design is wonderful. Not only is it fast but also quiet. What really sets it apart is the great battery life. In conclusion, highly recommend this to anyone looking for an upgrade.", true)
	f := Features(machine)
	if !f.MachineLeaning || f.LLMPhrases < 2 {
		t.Errorf("machine prose not flagged: %+v", f)
	}
	human := review("h", "", "sooo good!! bought this for my dad and he loves it... battery lasted like 3 days lol. one thing tho, the cable is SHORT. still, 5 stars, would buy again, no regrets at all honestly, fits the bag easily and the kids fight over it every single morning", true)
	f = Features(human)
	if f.MachineLeaning || f.Informality == 0 {
		t.Errorf("human prose flagged: %+v", f)
	}
}

func TestGuidelines(t *testing.T) {
	cases := map[string]string{
		"seller-order-shipping": "The package arrived damaged and the seller did not respond.",
		"pricing-comparison":    "It's cheaper at Mediamarkt, bought it there.",
		"private-information":   "Call me at 612 345 678 for details.",
		"links-external":        "More at https://example.com/deal?tag=abc",
		"compensated":           "I received this product for free in exchange for an honest review.",
		"promotional-conflict":  "Use my discount code SAVE20 at checkout.",
		"profanity-harassment":  "This is shit and the maker is an asshole.",
		"repetitive-spam":       "goooooooooood good good good good good",
	}
	for want, body := range cases {
		fs := Check([]product.Review{review("x", "", body, true)})
		hit := false
		for _, f := range fs {
			if f.Rule == want {
				hit = true
				if f.Citation == "" || f.Matched == "" {
					t.Errorf("%s: finding lacks citation or match", want)
				}
			}
		}
		if !hit {
			t.Errorf("%s did not fire on %q (got %+v)", want, body, fs)
		}
	}
	clean := []string{
		"Works as described. Battery lasts two days with heavy use.",
		"The sound is a little thin for classical but fine for podcasts.",
		"See the Amazon listing for the matching case: https://www.amazon.es/dp/B000000000",
	}
	for _, body := range clean {
		if fs := Check([]product.Review{review("x", "", body, true)}); len(fs) != 0 {
			t.Errorf("clean body fired %+v", fs)
		}
	}
	vine := review("v", "", "I received this product for free in exchange for an honest review.", true)
	vine.Vine = true
	for _, f := range Check([]product.Review{vine}) {
		if f.Rule == "compensated" {
			t.Error("Vine must be exempt from the compensation rule")
		}
	}
	dup := "This exact text appears twice under two different reviewers, long enough to count as a body."
	a, b := review("a", "", dup, true), review("b", "", dup, true)
	found := false
	for _, f := range Check([]product.Review{a, b}) {
		if f.Rule == "plagiarism-duplicate" {
			found = true
		}
	}
	if !found {
		t.Error("identical bodies across reviewers not flagged")
	}
	b.ProfileID = a.ProfileID
	for _, f := range Check([]product.Review{a, b}) {
		if f.Rule == "plagiarism-duplicate" {
			t.Error("same reviewer repeating is not plagiarism")
		}
	}
}
