package reviews

import (
	"regexp"
	"strings"

	"github.com/jgalea/amz/internal/product"
)

// Rules and citations are transcribed from Amazon's Community
// Guidelines help page (nodeId=GLHXEX85MENUE4XF), retrieved 2026-08-05.
// Every hit is a candidate, not a verdict: regexes over short
// multilingual text produce false positives, so each finding carries the
// matched span and a confidence.

type Finding struct {
	Rule       string `json:"rule"`
	RuleTitle  string `json:"rule_title"`
	Citation   string `json:"citation"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	ReviewID   string `json:"review_id"`
	Matched    string `json:"matched"`
	Excerpt    string `json:"excerpt"`
}

type rule struct {
	id, title, citation, severity, confidence string
	re                                        *regexp.Regexp
}

func compile(patterns ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:` + strings.Join(patterns, ")|(?:") + `)`)
}

var rules = []rule{
	{"seller-order-shipping", "Seller, order and shipping feedback",
		"Amazon does not allow reviews that focus on sellers and their customer service, ordering issues and returns, shipping and packaging, product condition and damage, or shipping cost and speed. Community content is meant to be about the product itself.",
		"medium", "weak", compile(
			`\barrived (?:late|damaged|broken|crushed|dented)\b`,
			`\b(?:package|packaging|parcel|box) (?:was|arrived|came) (?:damaged|crushed|open|torn|destroyed)\b`,
			`\b(?:fast|quick|slow|late|delayed) (?:delivery|shipping|dispatch)\b`,
			`\bnever (?:arrived|delivered|received)\b`,
			`\b(?:seller|vendor) (?:did not|didn't|refused to|never) (?:respond|reply|help|refund)\b`,
			`\bcustomer (?:service|support) was\b`,
			`\breturn(?:ed|ing)? (?:it|this|the item) (?:for a refund|straight away|immediately)\b`,
			`\blleg[óo] (?:tarde|roto|rota|dañado|dañada|estropeado)\b`,
			`\b(?:el )?(?:paquete|embalaje|caja) (?:lleg[óo]|ven[íi]a|estaba) (?:roto|rota|abierto|abierta|dañado|dañada|destrozado)\b`,
			`\benv[íi]o (?:r[áa]pido|lento|tard[íi]o|retrasado)\b`,
			`\bno (?:me )?(?:ha )?lleg(?:[óo]|ado)\b`,
			`\b(?:el )?vendedor no (?:responde|respondi[óo]|contest[óo]|quiso)\b`,
			`\bkam (?:beschädigt|kaputt|zu spät|verspätet)\b`,
			`\b(?:verpackung|paket) war (?:beschädigt|kaputt|offen)\b`,
			`\b(?:schnelle|langsame|verspätete) (?:lieferung|versand)\b`,
			`\bnie (?:angekommen|geliefert)\b`,
			`\bverkäufer (?:antwortet nicht|hat nicht geantwortet)\b`,
			`\b(?:colis|emballage) (?:endommagé|abîmé|ouvert|cassé)\b`,
			`\blivraison (?:rapide|lente|tardive|en retard)\b`,
			`\bjamais (?:reçu|livré)\b`,
			`\b(?:pacco|imballaggio|confezione) (?:danneggiat|rott|apert)\w*\b`,
			`\bconsegna (?:rapida|veloce|lenta|in ritardo)\b`,
			`\bmai (?:arrivato|ricevuto)\b`,
			`\b(?:pakket|verpakking) (?:beschadigd|kapot|open)\b`,
			`\b(?:snelle|trage|late) (?:levering|verzending)\b`,
		)},
	{"pricing-comparison", "Comments about pricing and availability",
		"Commenting on price is allowed when it speaks to the product's value. Pricing comments tied to an individual's own experience are not, and Amazon specifically calls out comparing the price of the same product at different stores.",
		"low", "weak", compile(
			`\b(?:cheaper|less|lower price) (?:at|in|on|from) (?:my local|the local|[A-Z]\w+|another (?:store|shop|site))\b`,
			`\bfound (?:it|this) (?:for )?(?:£|€|\$)?\d+(?:[.,]\d+)? (?:less|cheaper)\b`,
			`\bbought (?:it|this) (?:cheaper|for less) (?:at|in|from)\b`,
			`\bm[áa]s barato en (?:mi tienda|otra tienda|otro sitio|[A-Z]\w+)\b`,
			`\blo (?:encontr[ée]|vi|compr[ée]) m[áa]s barato en\b`,
			`\b(?:billiger|günstiger) (?:bei|im) (?:laden|geschäft|[A-Z]\w+)\b`,
			`\b(?:moins cher|meilleur prix) (?:chez|à|au) [A-Z]?\w+\b`,
			`\b(?:più economico|costa meno) (?:da|in|presso) [A-Z]?\w+\b`,
		)},
	{"private-information", "Private information",
		"Reviews must not share private information, including phone numbers, email addresses, mailing addresses, licence plates, DSNs, or order numbers.",
		"high", "strong", compile(
			`\b[\w.+-]+@[\w-]+\.[\w.]{2,}\b`,
			`\b(?:\+\d{1,3}[\s.-]?)?(?:\(\d{2,4}\)[\s.-]?)?\d{3}[\s.-]?\d{3}[\s.-]?\d{3,4}\b`,
			`\b\d{3}-\d{7}-\d{7}\b`,
			`\b(?:order number|n[uú]mero de pedido|bestellnummer|num[ée]ro de commande|numero d'ordine)\b`,
		)},
	{"links-external", "Links",
		"Links to other Amazon products are allowed; links to external sites are not. URLs carrying referrer tags or affiliate codes are never allowed.",
		"high", "strong", compile(
			`https?://(?:www\.)?[\w-]+(?:\.[\w-]+)*\.[a-z]{2,}(?:/\S*)?`,
			`\bwww\.[\w-]+\.[a-z]{2,}\b`,
			`[?&](?:tag|ref|aff|affiliate|utm_source)=[\w-]+`,
		)},
	{"compensated", "Compensated reviews",
		"Amazon does not allow reviews created, edited or removed in exchange for compensation, which includes cash, discounts, free products, gift cards and refunds. The only exceptions are Amazon Vine (labelled by Amazon) and advanced reader copies of books where no review was required in exchange.",
		"high", "strong", compile(
			`\b(?:received|got|given) (?:this|the|a|it) (?:product |item |sample )?(?:for )?free\b`,
			`\bin exchange for (?:an?|my) (?:honest |unbiased |candid )?(?:review|opinion)\b`,
			`\bat a discount(?:ed price)? in (?:exchange|return) for\b`,
			`\b(?:free|discounted) (?:product|sample) in (?:exchange|return) for\b`,
			`\b(?:gift card|voucher|refund|reimburs\w+) (?:for|in exchange for|after) (?:a |my |the )?(?:positive )?review\b`,
			`\btest(?:ed|er|ing) (?:product|sample) (?:provided|supplied|sent) (?:free|by the (?:brand|seller))\b`,
			`\b(?:recib[íi]|me (?:han )?(?:enviado|regalado)) (?:el|este|un) producto (?:gratis|gratuito|de forma gratuita|sin coste)\b`,
			`\ba cambio de (?:una |mi )?(?:rese[ñn]a|opini[óo]n|valoraci[óo]n)\b`,
			`\b(?:producto|muestra) (?:gratis|gratuito) a cambio\b`,
			`\bcon descuento a cambio de\b`,
			`\b(?:kostenlos|gratis) (?:erhalten|bekommen|zur verf[üu]gung gestellt)\b`,
			`\bim (?:austausch|gegenzug) f[üu]r eine (?:bewertung|rezension)\b`,
			`\bals? (?:testprodukt|gegenleistung f[üu]r eine rezension)\b`,
			`\bre[çc]u (?:ce produit )?gratuitement\b`,
			`\ben [ée]change d'un (?:avis|commentaire)\b`,
			`\bricevuto (?:questo prodotto |il prodotto )?(?:gratis|gratuitamente|in omaggio)\b`,
			`\bin cambio di una recensione\b`,
			`\bgratis ontvangen in ruil voor\b`,
		)},
	{"promotional-conflict", "Ads, conflicts of interest, promotional content",
		"Content whose main purpose is to promote a company, website, author or special offer is not allowed, nor is content about your own products or those of friends, relatives, employers, business associates or competitors. Reviews from anyone with a direct or indirect financial interest are removed.",
		"high", "weak", compile(
			`\bI (?:represent|work for|am the (?:seller|brand|manufacturer|author))\b`,
			`\bas the (?:seller|manufacturer|brand owner|author) of this\b`,
			`\bmy (?:own )?(?:company|brand|shop|store) (?:makes|sells|produces)\b`,
			`\b(?:use|apply) (?:my |the )?(?:discount |promo |coupon )?code [A-Z0-9]{4,}\b`,
			`\bcheck out my\b`,
			`\b(?:soy|somos) (?:el|la|los) (?:vendedor|fabricante|marca|autor)\b`,
			`\busa (?:el |mi )?c[óo]digo (?:de descuento )?[A-Z0-9]{4,}\b`,
			`\bich (?:vertrete|arbeite f[üu]r)\b`,
			`\bje (?:repr[ée]sente|travaille pour)\b`,
			`\brappresento (?:il|la) (?:marchio|venditore)\b`,
		)},
	{"profanity-harassment", "Profanity, harassment",
		"Profanity, obscenities and name-calling are not allowed, nor are harassment, threats, attacks on people you disagree with, or libel and inflammatory content.",
		"medium", "weak", compile(
			`\b(?:fuck\w*|shit\w*|bastard|asshole|bitch|cunt|wanker)\b`,
			`\b(?:mierda|gilipollas|cabr[óo]n|puta|joder|imb[ée]cil)\b`,
			`\b(?:schei[ßs]\w*|arschloch|wichser)\b`,
			`\b(?:merde|connard|salope|enfoir[ée])\b`,
			`\b(?:merda|stronzo|coglione|puttana)\b`,
			`\b(?:klootzak|kut|godverdomme)\b`,
		)},
}

var amazonLink = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?amazon\.`)

var spamRule = rule{"repetitive-spam", "Repetitive text, spam, pictures created with symbols",
	"Contributions with distracting content and spam are not allowed, including repetitive text, nonsense or gibberish, content that is just punctuation or symbols, and ASCII art.",
	"medium", "strong", nil}

var plagiarismRule = rule{"plagiarism-duplicate", "Plagiarism, infringement, impersonation",
	"Only your own content may be posted. Identical review text appearing under different reviewers is a signal of copied or templated content.",
	"high", "strong", nil}

func excerpt(body string, start, end int) string {
	r := []rune(body)
	s, e := start-45, end+45
	if s < 0 {
		s = 0
	}
	if e > len(r) {
		e = len(r)
	}
	out := strings.TrimSpace(string(r[s:e]))
	if s > 0 {
		out = "..." + out
	}
	if e < len(r) {
		out += "..."
	}
	return out
}

func finding(ru rule, r product.Review, matched, ex string) Finding {
	if len([]rune(matched)) > 80 {
		matched = string([]rune(matched)[:80])
	}
	return Finding{ru.id, ru.title, ru.citation, ru.severity, ru.confidence, r.ID, matched, ex}
}

// spam reports one char ten times, one word five times in a row, or a
// body of nothing but symbols.
func spam(text string) (string, bool) {
	runes := []rune(text)
	run := 1
	for i := 1; i < len(runes); i++ {
		if runes[i] == runes[i-1] {
			run++
			if run >= 10 {
				return string(runes[i-9 : i+1]), true
			}
		} else {
			run = 1
		}
	}
	words := strings.Fields(strings.ToLower(nonWord.ReplaceAllString(text, " ")))
	run = 1
	for i := 1; i < len(words); i++ {
		if len(words[i]) >= 3 && words[i] == words[i-1] {
			run++
			if run >= 5 {
				return strings.Join(words[i-4:i+1], " "), true
			}
		} else {
			run = 1
		}
	}
	t := strings.TrimSpace(text)
	if len([]rune(t)) >= 12 && !regexp.MustCompile(`[\p{L}\p{N}]`).MatchString(t) {
		return t, true
	}
	return "", false
}

// Check runs every guideline rule over a set of reviews.
func Check(rs []product.Review) []Finding {
	var out []Finding
	for _, ru := range rules {
		for _, r := range rs {
			hay := strings.TrimSpace(r.Title + "\n" + r.Body)
			if hay == "" {
				continue
			}
			// Vine is Amazon's own labelled exception to the compensation rule.
			if ru.id == "compensated" && r.Vine {
				continue
			}
			loc := ru.re.FindStringIndex(hay)
			if loc == nil {
				continue
			}
			m := hay[loc[0]:loc[1]]
			if ru.id == "links-external" && amazonLink.MatchString(m) {
				continue
			}
			out = append(out, finding(ru, r, m, excerpt(hay, len([]rune(hay[:loc[0]])), len([]rune(hay[:loc[1]])))))
		}
	}
	for _, r := range rs {
		hay := r.Title + "\n" + r.Body
		if m, ok := spam(hay); ok {
			out = append(out, finding(spamRule, r, m, excerpt(hay, 0, 60)))
		}
	}
	seen := map[string]product.Review{}
	for _, r := range rs {
		k := strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(r.Body), ""))
		if len(k) < 40 {
			continue
		}
		if twin, ok := seen[k]; ok && twin.ProfileID != r.ProfileID {
			out = append(out, finding(plagiarismRule, r, "identical to "+twin.ID, excerpt(r.Body, 0, 75)))
			continue
		}
		seen[k] = r
	}
	return out
}
