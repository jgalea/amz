package reviews

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/jgalea/amz/internal/product"
)

// These are features, not a classifier. Real people write tidy reviews
// and models can be prompted to write messy ones, so no threshold here
// is conclusive on its own. The strongest tells in practice are low
// sentence-length variance, no informality markers at all, and the
// stock LLM connectives.

var (
	sentenceSplit = regexp.MustCompile(`[.!?¡¿]+\s+`)
	wordRe        = regexp.MustCompile(`[\p{L}\p{N}']+`)
	llmPhrases    = regexp.MustCompile(`(?i)` + strings.Join([]string{
		`\boverall,`, `\bin conclusion\b`, `\bthat said,`, `\bwhat (?:really )?(?:sets it apart|stands out)\b`,
		`\bnot only .{3,40} but also\b`, `\bis a game[- ]changer\b`, `\belevat(?:es|ed|ing) (?:my|the|your)\b`,
		`\ba testament to\b`, `\bwhether you(?:'re| are) .{3,40} or\b`, `\bhighly recommend (?:this|it) (?:to anyone|for anyone)\b`,
		`\ben resumen,`, `\ben conclusi[óo]n\b`, `\bdicho esto,`, `\bno solo .{3,40} sino (?:que )?tambi[ée]n\b`,
		`\blo recomiendo (?:encarecidamente|totalmente) a cualquiera\b`, `\bmerece la pena cada c[ée]ntimo\b`,
		`\binsgesamt,`, `\bzusammenfassend\b`, `\bnicht nur .{3,40} sondern auch\b`,
		`\ben r[ée]sum[ée],`, `\bnon seulement .{3,40} mais aussi\b`, `\bin conclusione\b`, `\bnon solo .{3,40} ma anche\b`,
	}, "|"))
	informal = regexp.MustCompile(strings.Join([]string{
		`\b(?:lol|omg|meh|ugh|hmm+|jaja+|jeje+|haha+|ahah+)\b`,
		`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]`, `\.{3,}`, `!{2,}`, `\b[A-ZÀ-Ý]{4,}\b`, `\s{2,}\S`,
	}, "|"))
	genericPraise = regexp.MustCompile(`(?i)\b(?:great|excellent|amazing|perfect|fantastic|wonderful|superb|awesome|excelente|perfecto|estupendo|fant[áa]stico|maravilloso|genial|ausgezeichnet|hervorragend|perfekt|wunderbar|parfait|formidable|eccellente|perfetto|fantastico|meraviglioso|uitstekend|geweldig)\b`)
)

type TextFeatures struct {
	ReviewID       string   `json:"review_id"`
	Words          int      `json:"words"`
	Sentences      int      `json:"sentences"`
	MeanSentence   float64  `json:"mean_sentence_words,omitempty"`
	SentenceStdev  float64  `json:"sentence_stdev,omitempty"`
	TypeTokenRatio float64  `json:"type_token_ratio,omitempty"`
	LLMPhrases     int      `json:"llm_phrases"`
	LLMPhraseHits  []string `json:"llm_phrase_hits,omitempty"`
	Informality    int      `json:"informality_markers"`
	GenericPraise  int      `json:"generic_praise"`
	PraiseDensity  float64  `json:"generic_praise_density,omitempty"`
	EmDashes       int      `json:"em_dashes"`
	CurlyQuotes    int      `json:"curly_quotes"`
	MachineLeaning bool     `json:"machine_leaning"`
	Reasons        []string `json:"reasons,omitempty"`
}

// Features measures one review's prose.
func Features(r product.Review) TextFeatures {
	body := r.Body
	words := wordRe.FindAllString(body, -1)
	f := TextFeatures{ReviewID: r.ID, Words: len(words)}

	var lens []float64
	for _, s := range sentenceSplit.Split(body, -1) {
		if n := len(wordRe.FindAllString(s, -1)); n > 0 {
			lens = append(lens, float64(n))
		}
	}
	f.Sentences = len(lens)
	if len(lens) > 0 {
		sum := 0.0
		for _, l := range lens {
			sum += l
		}
		f.MeanSentence = math.Round(sum/float64(len(lens))*100) / 100
		if len(lens) >= 3 {
			mean := sum / float64(len(lens))
			v := 0.0
			for _, l := range lens {
				v += (l - mean) * (l - mean)
			}
			f.SentenceStdev = math.Round(math.Sqrt(v/float64(len(lens)))*100) / 100
		}
	}
	if len(words) > 0 {
		uniq := map[string]bool{}
		for _, w := range words {
			uniq[strings.ToLower(w)] = true
		}
		f.TypeTokenRatio = math.Round(float64(len(uniq))/float64(len(words))*1000) / 1000
	}
	for _, m := range llmPhrases.FindAllString(body, -1) {
		if len(m) > 40 {
			m = m[:40]
		}
		f.LLMPhraseHits = append(f.LLMPhraseHits, m)
	}
	f.LLMPhrases = len(f.LLMPhraseHits)
	if len(f.LLMPhraseHits) > 5 {
		f.LLMPhraseHits = f.LLMPhraseHits[:5]
	}
	f.Informality = len(informal.FindAllString(body, -1)) + stretched(body)
	f.GenericPraise = len(genericPraise.FindAllString(body, -1))
	if len(words) > 0 {
		f.PraiseDensity = math.Round(float64(f.GenericPraise)/float64(len(words))*10000) / 10000
	}
	f.EmDashes = strings.Count(body, "—")
	f.CurlyQuotes = strings.Count(body, "“") + strings.Count(body, "”") + strings.Count(body, "’")

	// Only judge prose long enough to have a shape.
	if len(words) >= 40 {
		if len(lens) >= 4 && f.SentenceStdev < 3.0 {
			f.Reasons = append(f.Reasons, fmt.Sprintf("uniform sentence lengths (stdev %.1f words)", f.SentenceStdev))
		}
		if f.Informality == 0 {
			f.Reasons = append(f.Reasons, "no typos, emoji, ellipses or shouting anywhere")
		}
		if f.LLMPhrases > 0 {
			f.Reasons = append(f.Reasons, "stock LLM connectives: "+strings.Join(f.LLMPhraseHits, ", "))
		}
		if f.PraiseDensity > 0.06 {
			f.Reasons = append(f.Reasons, fmt.Sprintf("generic praise is %.0f%% of all words", f.PraiseDensity*100))
		}
		if f.EmDashes >= 2 {
			f.Reasons = append(f.Reasons, "repeated em dashes, rare in typed review text")
		}
	}
	f.MachineLeaning = len(f.Reasons) >= 2
	return f
}

func AnalyseText(rs []product.Review) []TextFeatures {
	out := make([]TextFeatures, 0, len(rs))
	for _, r := range rs {
		out = append(out, Features(r))
	}
	return out
}

// stretched counts runs of three or more of the same letter ("sooo",
// "yesss"), which RE2 cannot express with a backreference.
func stretched(s string) int {
	n, run := 0, 1
	var prev rune
	for _, r := range strings.ToLower(s) {
		if r == prev && r >= 'a' && r <= 'z' {
			run++
			if run == 3 {
				n++
			}
		} else {
			run = 1
		}
		prev = r
	}
	return n
}
