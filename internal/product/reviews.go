package product

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Review is one of the roughly dozen reviews Amazon renders on a public
// detail page. The paginated review endpoints are behind a sign-in wall,
// so this is the ceiling for unauthenticated collection.
type Review struct {
	ID          string  `json:"review_id"`
	Title       string  `json:"title,omitempty"`
	Body        string  `json:"body"`
	Rating      float64 `json:"rating,omitempty"`
	Date        string  `json:"date,omitempty"`
	DateRaw     string  `json:"date_raw,omitempty"`
	Country     string  `json:"country,omitempty"`
	Verified    bool    `json:"verified"`
	Vine        bool    `json:"vine"`
	ProfileID   string  `json:"profile_id,omitempty"`
	ProfileName string  `json:"profile_name,omitempty"`
	Variation   string  `json:"variation,omitempty"`
	Helpful     int     `json:"helpful_votes"`
	HasMedia    bool    `json:"has_media"`
	BodyChars   int     `json:"body_chars"`
}

type ReviewSet struct {
	Reviews  []Review `json:"reviews"`
	Warnings []string `json:"warnings,omitempty"`
}

var (
	starsRe    = regexp.MustCompile(`(\d+[.,]?\d*)\s*(?:de|out of|von|su|sur|van|z|av)\s*5`)
	helpfulRe  = regexp.MustCompile(`[\d.,]+`)
	profileRe  = regexp.MustCompile(`(amzn1\.account\.[A-Z0-9]+)`)
	countryRe  = regexp.MustCompile(`(?:Reseñado en|Reviewed in|Rezension aus|Commenté en|Recensito in|Beoordeeld in|Avaliado em|Recenzja z|Recenserad i)\s+(.+?)\s+(?:el|on|am|le|il|op|em|w dniu|den)\s`)
	dateDMYRe  = regexp.MustCompile(`(\d{1,2})\s+(?:de\s+)?([^\s\d]+)\s+(?:de\s+)?(\d{4})`)
	dateMDYRe  = regexp.MustCompile(`([A-Za-z]+)\s+(\d{1,2}),\s*(\d{4})`)
	teaserText = []string{"Brief content visible", "Full content visible", "double tap to read"}
)

var reviewMonths = map[string]int{}

func init() {
	for _, lang := range [][]string{
		strings.Fields("january february march april may june july august september october november december"),
		strings.Fields("enero febrero marzo abril mayo junio julio agosto septiembre octubre noviembre diciembre"),
		strings.Fields("januar februar märz april mai juni juli august september oktober november dezember"),
		strings.Fields("janvier février mars avril mai juin juillet août septembre octobre novembre décembre"),
		strings.Fields("gennaio febbraio marzo aprile maggio giugno luglio agosto settembre ottobre novembre dicembre"),
		strings.Fields("januari februari maart april mei juni juli augustus september oktober november december"),
		strings.Fields("janeiro fevereiro março abril maio junho julho agosto setembro outubro novembro dezembro"),
	} {
		for i, m := range lang {
			reviewMonths[m] = i + 1
		}
	}
}

func parseReviewDate(raw string) string {
	if m := dateDMYRe.FindStringSubmatch(raw); m != nil {
		if mon, ok := reviewMonths[strings.ToLower(strings.TrimRight(m[2], "."))]; ok {
			d, _ := strconv.Atoi(m[1])
			return isoDate(m[3], mon, d)
		}
	}
	if m := dateMDYRe.FindStringSubmatch(raw); m != nil {
		if mon, ok := reviewMonths[strings.ToLower(m[1])]; ok {
			d, _ := strconv.Atoi(m[2])
			return isoDate(m[3], mon, d)
		}
	}
	return ""
}

func isoDate(year string, mon, day int) string {
	if day < 1 || day > 31 {
		return ""
	}
	return year + "-" + pad(mon) + "-" + pad(day)
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func cleanBody(s *goquery.Selection) string {
	if s.Length() == 0 {
		return ""
	}
	s = s.Clone()
	s.Find(".a-teaser-describedby-collapsed, .a-teaser-describedby-expanded, script, style").Remove()
	t := s.Text()
	for _, phrase := range teaserText {
		t = strings.ReplaceAll(t, phrase, " ")
	}
	return strings.Join(strings.Fields(t), " ")
}

// ParseReviews reads the reviews rendered on a detail page.
func ParseReviews(html string) ReviewSet {
	var out ReviewSet
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		out.Warnings = append(out.Warnings, "could not parse the HTML: "+err.Error())
		return out
	}
	nodes := doc.Find(`div[data-hook="review"]`)
	if nodes.Length() == 0 {
		out.Warnings = append(out.Warnings, "no review nodes matched div[data-hook='review']; Amazon markup may have changed")
		return out
	}
	nodes.Each(func(_ int, n *goquery.Selection) {
		r := Review{}
		r.ID, _ = n.Attr("id")
		r.Body = cleanBody(n.Find(`[data-hook="reviewText"]`))
		r.BodyChars = len([]rune(r.Body))
		r.Title = text(n.Find(`[data-hook="review-title"] span:not(.a-letter-space), [data-hook="reviewTitle"]`).Last())
		if r.Title == "" {
			r.Title = text(n.Find(`[data-hook="review-title"]`))
		}
		r.DateRaw = text(n.Find(`[data-hook="review-date"]`))
		r.Date = parseReviewDate(r.DateRaw)
		if m := countryRe.FindStringSubmatch(r.DateRaw); m != nil {
			r.Country = strings.TrimSpace(m[1])
		}
		if m := starsRe.FindStringSubmatch(text(n.Find(`[data-hook="review-star-rating"], [data-hook="cmps-review-star-rating"]`))); m != nil {
			r.Rating, _ = strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		}
		badges := n.Find(`[data-hook="review-badges"]`)
		if badges.Length() == 0 {
			badges = n
		}
		r.Vine = strings.Contains(strings.ToLower(text(badges)), "vine")
		r.Verified = n.Find(`[data-hook="avp-badge"]`).Length() > 0
		if href, ok := n.Find("a.a-profile").Attr("href"); ok {
			if m := profileRe.FindStringSubmatch(href); m != nil {
				r.ProfileID = m[1]
			}
		}
		r.ProfileName = text(n.Find(".a-profile-name"))
		r.Variation = text(n.Find(`[data-hook="format-strip"]`))
		if t := text(n.Find(`[data-hook="helpful-vote-statement"]`)); t != "" {
			r.Helpful = 1
			if m := helpfulRe.FindString(t); m != "" {
				if v, ok := Int(m); ok {
					r.Helpful = v
				}
			}
		}
		r.HasMedia = n.Find(`[data-hook="reviewVideo"], .review-image-tile`).Length() > 0
		out.Reviews = append(out.Reviews, r)
	})
	empty, undated := 0, 0
	for _, r := range out.Reviews {
		if r.Body == "" {
			empty++
		}
		if r.Date == "" {
			undated++
		}
	}
	if empty > 0 {
		out.Warnings = append(out.Warnings, strconv.Itoa(empty)+"/"+strconv.Itoa(len(out.Reviews))+" reviews parsed with an empty body")
	}
	if undated > 0 {
		out.Warnings = append(out.Warnings, strconv.Itoa(undated)+"/"+strconv.Itoa(len(out.Reviews))+" reviews had an unparseable date")
	}
	return out
}
