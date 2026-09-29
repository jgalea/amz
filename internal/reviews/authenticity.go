// Package reviews holds the review audit: set-level authenticity
// signals, per-review prose features and Amazon Community Guidelines
// checks. Nothing here returns a verdict; each signal reports its value,
// how many items it was computed over and whether it reads as
// suspicious. A signal with too small an n reports Evaluated=false
// rather than a confident-looking zero, because an empty input must
// never render as a pass.
package reviews

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/product"
)

const MinN = 4

type Signal struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	Value      float64 `json:"value"`
	Evaluated  bool    `json:"evaluated"`
	N          int     `json:"n"`
	Suspicious bool    `json:"suspicious"`
	Note       string  `json:"note"`
}

type Authenticity struct {
	Signals    []Signal `json:"signals"`
	Notes      []string `json:"notes,omitempty"`
	Suspicious int      `json:"suspicious_count"`
	Scored     int      `json:"scored_count"`
}

func (a *Authenticity) add(s Signal) {
	a.Signals = append(a.Signals, s)
	if s.Evaluated {
		a.Scored++
	}
	if s.Suspicious {
		a.Suspicious++
	}
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}\s]`)

func shingles(text string, k int) map[string]bool {
	words := strings.Fields(nonWord.ReplaceAllString(strings.ToLower(text), " "))
	out := map[string]bool{}
	if len(words) < k {
		if len(words) > 0 {
			out[strings.Join(words, " ")] = true
		}
		return out
	}
	for i := 0; i+k <= len(words); i++ {
		out[strings.Join(words[i:i+k], " ")] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// Analyse computes the set-level signals for a product's review sample.
func Analyse(p product.Product, rs []product.Review) Authenticity {
	var out Authenticity
	n := len(rs)
	if n == 0 {
		out.Notes = append(out.Notes, "no reviews parsed; every signal is unevaluated, not clean")
		return out
	}
	if n < MinN {
		out.Notes = append(out.Notes, fmt.Sprintf("only %d reviews parsed; set-level signals need at least %d", n, MinN))
	}

	verified := 0
	for _, r := range rs {
		if r.Verified {
			verified++
		}
	}
	ratio := float64(verified) / float64(n)
	out.add(Signal{"verified_ratio", "Verified-purchase share", round3(ratio), true, n, n >= MinN && ratio < 0.6, fmt.Sprintf("%d/%d carry a verified-purchase badge", verified, n)})

	vine := 0
	for _, r := range rs {
		if r.Vine {
			vine++
		}
	}
	out.add(Signal{"vine_share", "Vine reviews in sample", round3(float64(vine) / float64(n)), true, n, n >= MinN && float64(vine)/float64(n) > 0.4, fmt.Sprintf("%d/%d are Amazon Vine (free product, disclosed and allowed)", vine, n)})

	var dates []time.Time
	for _, r := range rs {
		if t, err := time.Parse("2006-01-02", r.Date); err == nil {
			dates = append(dates, t)
		}
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	if len(dates) >= MinN {
		worst := 0
		for i, start := range dates {
			count := 0
			for _, d := range dates[i:] {
				if d.Sub(start) <= 7*24*time.Hour {
					count++
				}
			}
			if count > worst {
				worst = count
			}
		}
		burst := float64(worst) / float64(len(dates))
		out.add(Signal{"date_burst", "Largest 7-day cluster", round3(burst), true, len(dates), burst > 0.5 && len(dates) >= 6, fmt.Sprintf("%d/%d dated reviews fall inside one 7-day window", worst, len(dates))})
		span := int(dates[len(dates)-1].Sub(dates[0]).Hours() / 24)
		out.add(Signal{"date_span_days", "Days covered by sample", float64(span), true, len(dates), span < 14 && len(dates) >= 6, fmt.Sprintf("sample runs %s to %s", dates[0].Format("2006-01-02"), dates[len(dates)-1].Format("2006-01-02"))})
	} else {
		out.add(Signal{"date_burst", "Largest 7-day cluster", 0, false, len(dates), false, "too few parseable dates to evaluate"})
	}

	// Genuine listings taper 5>4>3>2>1; a deep 4-star trough beside a
	// tall 1-star bar is the classic manipulated profile.
	if len(p.Histogram) >= 4 {
		total := 0
		for _, v := range p.Histogram {
			total += v
		}
		if total > 0 {
			five := float64(p.Histogram[5]) / float64(total)
			four := float64(p.Histogram[4]) / float64(total)
			three := float64(p.Histogram[3]) / float64(total)
			one := float64(p.Histogram[1]) / float64(total)
			out.add(Signal{"five_star_share", "Share of 5-star ratings", round3(five), true, total, five > 0.85, fmt.Sprintf("%d%% of all ratings are 5-star", p.Histogram[5])})
			trough := four < three && one > four
			note := "tapers normally from 5-star downward"
			if trough {
				note = "4-star bar sits below 3-star while 1-star exceeds it, a bimodal profile"
			}
			out.add(Signal{"distribution_shape", "Rating curve shape", round3(four - one), true, total, trough, note})
		}
	} else {
		out.add(Signal{"five_star_share", "Share of 5-star ratings", 0, false, 0, false, "histogram not parsed"})
	}

	type body struct {
		sh map[string]bool
	}
	var bodies []body
	for _, r := range rs {
		if len(r.Body) > 60 {
			bodies = append(bodies, body{shingles(r.Body, 5)})
		}
	}
	if len(bodies) >= 2 {
		pairs, worst := 0, 0.0
		for i := range bodies {
			for j := i + 1; j < len(bodies); j++ {
				sim := jaccard(bodies[i].sh, bodies[j].sh)
				if sim > worst {
					worst = sim
				}
				if sim > 0.5 {
					pairs++
				}
			}
		}
		out.add(Signal{"duplicate_pairs", "Near-duplicate review pairs", float64(pairs), true, len(bodies), pairs > 0, fmt.Sprintf("%d pair(s) over 50%% shingle overlap; highest overlap %.0f%%", pairs, worst*100)})
	} else {
		out.add(Signal{"duplicate_pairs", "Near-duplicate review pairs", 0, false, len(bodies), false, "fewer than two reviews long enough to compare"})
	}

	profiles := map[string]int{}
	for _, r := range rs {
		if r.ProfileID != "" {
			profiles[r.ProfileID]++
		}
	}
	if len(profiles) > 0 {
		repeats := 0
		for _, c := range profiles {
			if c > 1 {
				repeats += c - 1
			}
		}
		out.add(Signal{"repeat_reviewers", "Repeat reviewers in sample", float64(repeats), true, len(profiles), repeats > 0, fmt.Sprintf("%d distinct profiles across %d reviews", len(profiles), n)})
	}

	// Reviews attached to unrelated variations point at listing merges.
	variations := map[string]int{}
	for _, r := range rs {
		if r.Variation != "" {
			variations[r.Variation]++
		}
	}
	if len(variations) > 0 && p.VariationCount > 0 {
		total := 0
		for _, c := range variations {
			total += c
		}
		limit := p.VariationCount
		if limit < 3 {
			limit = 3
		}
		out.add(Signal{"variation_spread", "Distinct variations reviewed", float64(len(variations)), true, total, len(variations) > limit, fmt.Sprintf("%d variation strings across the sample; listing exposes %d variation options", len(variations), p.VariationCount)})
	}

	countries := map[string]int{}
	for _, r := range rs {
		if r.Country != "" {
			countries[r.Country]++
		}
	}
	if len(countries) > 0 {
		top, topN := "", 0
		for c, k := range countries {
			if k > topN || (k == topN && c < top) {
				top, topN = c, k
			}
		}
		foreign := n - topN
		out.add(Signal{"foreign_reviews", "Reviews from other marketplaces", round3(float64(foreign) / float64(n)), true, n, float64(foreign)/float64(n) > 0.5, fmt.Sprintf("most common origin %q (%d/%d); %d from elsewhere", top, topN, n, foreign)})
	}

	var lengths []int
	for _, r := range rs {
		if r.BodyChars > 0 {
			lengths = append(lengths, r.BodyChars)
		}
	}
	if len(lengths) >= MinN {
		short := 0
		for _, l := range lengths {
			if l < 80 {
				short++
			}
		}
		sort.Ints(lengths)
		out.add(Signal{"short_body_share", "Share of very short reviews", round3(float64(short) / float64(len(lengths))), true, len(lengths), float64(short)/float64(len(lengths)) > 0.6, fmt.Sprintf("median length %d chars; %d/%d under 80 chars", lengths[len(lengths)/2], short, len(lengths))})
	}
	return out
}
