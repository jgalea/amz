package amazon

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ordersReadySelector matches something that only exists once the order
// list has rendered: an order card, the empty-state container, or the
// time filter.
const ordersReadySelector = ".order-card, .js-order-card, #ordersContainer, select[name=timeFilter], #time-filter"

type Item struct {
	Title string `json:"title"`
	ASIN  string `json:"asin,omitempty"`
	URL   string `json:"url,omitempty"`
	Qty   string `json:"qty,omitempty"`
}

type Order struct {
	ID      string `json:"id"`
	Date    string `json:"date,omitempty"`
	DateISO string `json:"date_iso,omitempty"`
	Total   string `json:"total,omitempty"`
	// Status is the delivery text of each shipment, joined with "; ".
	Status string `json:"status,omitempty"`
	// Returns holds any return or refund wording found on the card.
	Returns    []string `json:"returns,omitempty"`
	Items      []Item   `json:"items"`
	URL        string   `json:"url,omitempty"`
	InvoiceURL string   `json:"invoice_url,omitempty"`
	// ReturnUntil is the "eligible for return until" date the card shows
	// for delivered items, as YYYY-MM-DD.
	ReturnUntil string `json:"return_until,omitempty"`
	// DeliveredISO is the delivery date read off the shipment status.
	DeliveredISO string `json:"delivered_iso,omitempty"`
}

var (
	orderIDRe = regexp.MustCompile(`\b(\d{3}-\d{7}-\d{7})\b`)
	asinRe    = regexp.MustCompile(`/(?:dp|gp/product|gp/aw/d)/([A-Z0-9]{10})(?:[/?]|$)`)
	// returnRe covers the return/refund wording on the ES, EN, DE, FR,
	// IT and PT storefronts.
	returnRe = regexp.MustCompile(`(?i)reembols|refund|devoluci|devolvid|devuelt|return|retour|rembours|rimbors|reso |erstatt|rückgabe|rücksend|zurückgeschickt|replacement|sustitu|remplac|sostitu|ersatz`)
	// returnWindowRe matches the "eligible for return until" line every
	// delivered item carries, which is not a return.
	returnWindowRe = regexp.MustCompile(`(?i)(hasta el|until|bis zum|jusqu'au|fino al|até)\b|window|plazo|ventana|fenster|elegible|eligible`)
	numOrdersRe    = regexp.MustCompile(`(\d+)`)
	// returnUntilRe is the open return window line ("Return or replace
	// items: Eligible until 14 October 2026", "Devolución elegible hasta el
	// ..."); a closed window says "closed"/"cerrada"/"geschlossen".
	returnUntilRe = regexp.MustCompile(`(?i)(eligible|elegible|possible|posible|möglich|admissible|idoneo|elegível)\b.*\b(until|hasta|bis|jusqu|fino|até)\b`)
	deliveredRe   = regexp.MustCompile(`(?i)^(delivered|entregado|entregue|livré|consegnato|zugestellt|geliefert|bezorgd)`)
)

// ParseOrders reads the order cards off one order-history page.
func ParseOrders(site Site, doc *goquery.Document) []Order {
	var out []Order
	cards := doc.Find(".order-card, .js-order-card")
	if cards.Length() == 0 {
		cards = doc.Find(".order")
	}
	seen := map[string]bool{}
	cards.Each(func(_ int, card *goquery.Selection) {
		o := parseOrderCard(site, card)
		if o.ID == "" || seen[o.ID] {
			return
		}
		seen[o.ID] = true
		out = append(out, o)
	})
	return out
}

func parseOrderCard(site Site, card *goquery.Selection) Order {
	o := Order{Items: []Item{}}

	header := card.Find(".order-header, .order-info").First()
	if header.Length() == 0 {
		header = card
	}
	o.ID = findOrderID(header)
	if o.ID == "" {
		o.ID = findOrderID(card)
	}
	if o.ID == "" {
		return o
	}
	o.URL = site.OrderDetailsURL(o.ID)

	// The header lays out "label / value" column pairs: order placed,
	// total, ship to. Read the value under each label we care about.
	header.Find(".a-column").Each(func(_ int, col *goquery.Selection) {
		lines := Lines(col)
		if len(lines) < 2 {
			return
		}
		label := strings.ToLower(lines[0])
		switch {
		case isDateLabel(label):
			o.Date = lines[1]
		case isTotalLabel(label):
			o.Total = lines[1]
		}
	})
	if o.Date == "" || o.Total == "" {
		lines := Lines(header)
		for i := 0; i+1 < len(lines); i++ {
			label := strings.ToLower(lines[i])
			if o.Date == "" && isDateLabel(label) {
				o.Date = lines[i+1]
			}
			if o.Total == "" && isTotalLabel(label) {
				o.Total = lines[i+1]
			}
		}
	}
	o.DateISO = ParseDate(o.Date)

	card.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		pop, _ := a.Attr("data-a-popover")
		if strings.Contains(pop, "/invoice/invoice.html") {
			if m := regexp.MustCompile(`"url"\s*:\s*"([^"]+)"`).FindStringSubmatch(pop); m != nil {
				o.InvoiceURL = site.Absolute(strings.ReplaceAll(m[1], `\/`, `/`))
				return false
			}
		}
		if strings.Contains(href, "/invoice/invoice.html") {
			o.InvoiceURL = site.Absolute(href)
			return false
		}
		return true
	})

	var statuses []string
	statusSel := ".delivery-box__primary-text, .yohtmlc-shipment-status-primaryText, .shipment-top-row .a-size-medium, .js-shipment-info-container .a-size-medium"
	card.Find(statusSel).Each(func(_ int, s *goquery.Selection) {
		t := Text(s)
		if t != "" && !contains(statuses, t) {
			statuses = append(statuses, t)
		}
	})
	o.Status = strings.Join(statuses, "; ")

	seenItems := map[string]bool{}
	// Each item is a left/right grid: image (with quantity badge) on the
	// left, title and actions on the right. Prefer the whole grid so the
	// quantity is in scope; fall back to the right column on layouts
	// that do not wrap the two.
	boxes := card.Find(".item-box")
	if boxes.Length() == 0 {
		boxes = card.Find(".yohtmlc-item").Parent()
	}
	boxes.Each(func(_ int, box *goquery.Selection) {
		it, ok := parseItem(site, box)
		if !ok {
			return
		}
		key := it.ASIN + "|" + it.Title
		if seenItems[key] {
			return
		}
		seenItems[key] = true
		o.Items = append(o.Items, it)
	})
	if len(o.Items) == 0 {
		// Older layouts have no item box, only product links.
		card.Find("a[href*='/dp/'], a[href*='/gp/product/']").Each(func(_ int, a *goquery.Selection) {
			title := Text(a)
			if title == "" || a.Find("img").Length() > 0 {
				return
			}
			href, _ := a.Attr("href")
			it := Item{Title: title, ASIN: asinOf(href), URL: site.Absolute(stripRef(href)), Qty: "1"}
			key := it.ASIN + "|" + it.Title
			if seenItems[key] {
				return
			}
			seenItems[key] = true
			o.Items = append(o.Items, it)
		})
	}

	// Inline scripts on the card would otherwise match on the JS keyword.
	card.Find("script, style, noscript").Remove()
	for _, line := range Lines(card) {
		if returnRe.MatchString(line) && !returnWindowRe.MatchString(line) && !isReturnButton(line) && !contains(o.Returns, line) {
			o.Returns = append(o.Returns, line)
		}
		if o.ReturnUntil == "" && returnUntilRe.MatchString(line) {
			o.ReturnUntil = ParseDateNear(line, o.DateISO)
		}
	}
	for _, st := range statuses {
		if deliveredRe.MatchString(st) {
			if d := ParseDateNear(st, o.DateISO); d != "" {
				o.DeliveredISO = d
				break
			}
		}
	}
	return o
}

func parseItem(site Site, box *goquery.Selection) (Item, bool) {
	var it Item
	link := box.Find("a[href*='/dp/'], a[href*='/gp/product/']").FilterFunction(func(_ int, a *goquery.Selection) bool {
		return a.Find("img").Length() == 0 && Text(a) != ""
	}).First()
	if link.Length() == 0 {
		return it, false
	}
	href, _ := link.Attr("href")
	it.Title = Text(link)
	it.ASIN = asinOf(href)
	it.URL = site.Absolute(stripRef(href))
	it.Qty = "1"
	if q := Text(box.Find(".product-image__qty").First()); q != "" {
		it.Qty = q
	}
	if it.Title == "" || isReturnButton(it.Title) {
		return it, false
	}
	return it, true
}

func findOrderID(s *goquery.Selection) string {
	var id string
	s.Find("bdi, [dir=ltr]").EachWithBreak(func(_ int, el *goquery.Selection) bool {
		if m := orderIDRe.FindString(Text(el)); m != "" {
			id = m
			return false
		}
		return true
	})
	if id != "" {
		return id
	}
	s.Find("a[href*='orderID='], a[href*='orderId=']").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		if m := orderIDRe.FindString(href); m != "" {
			id = m
			return false
		}
		return true
	})
	if id != "" {
		return id
	}
	return orderIDRe.FindString(s.Text())
}

func isDateLabel(label string) bool {
	for _, k := range []string{"pedido realizado", "order placed", "bestellt am", "bestellung aufgegeben", "commande effectuée", "ordine effettuato", "pedido efetuado", "pedido feito", "placed"} {
		if strings.Contains(label, k) {
			return true
		}
	}
	return false
}

func isTotalLabel(label string) bool {
	for _, k := range []string{"total", "summe", "gesamt", "totale", "montant"} {
		if strings.Contains(label, k) {
			return true
		}
	}
	return false
}

// isReturnButton screens out the "Return or replace items" action
// button, which every delivered order shows and which says nothing
// about an actual return.
func isReturnButton(line string) bool {
	l := strings.ToLower(line)
	for _, k := range []string{"devolver o sustituir", "return or replace", "return items", "artikel zurücksenden", "retourner ou remplacer", "restituisci o sostituisci", "devolver ou substituir", "return & replace", "how to return", "cómo devolver", "return window", "returns are"} {
		if strings.HasPrefix(l, k) || l == k {
			return true
		}
	}
	return false
}

func asinOf(href string) string {
	if m := asinRe.FindStringSubmatch(href); m != nil {
		return m[1]
	}
	return ""
}

func stripRef(href string) string {
	if i := strings.Index(href, "?"); i >= 0 {
		href = href[:i]
	}
	if i := strings.Index(href, "/ref="); i >= 0 {
		href = href[:i]
	}
	return href
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ParseYears reads the years offered by the time-filter dropdown,
// newest first.
func ParseYears(doc *goquery.Document) []int {
	var years []int
	doc.Find("select[name=timeFilter] option, #time-filter option, #orderFilter option").Each(func(_ int, opt *goquery.Selection) {
		v, _ := opt.Attr("value")
		if strings.HasPrefix(v, "year-") {
			if y, err := strconv.Atoi(strings.TrimPrefix(v, "year-")); err == nil && !containsInt(years, y) {
				years = append(years, y)
			}
		}
	})
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	return years
}

// ParseOrderCount reads the "N orders placed in" line, or 0 when the
// page does not show one.
func ParseOrderCount(doc *goquery.Document) int {
	t := Text(doc.Find(".num-orders").First())
	if m := numOrdersRe.FindString(t); m != "" {
		n, _ := strconv.Atoi(m)
		return n
	}
	return 0
}

func containsInt(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

// Years opens the order list and returns the years the account has
// orders in, newest first.
func Years(ctx context.Context, site Site) ([]int, error) {
	p, err := Load(ctx, site.OrdersURL(0, 0), 20*time.Second, ordersReadySelector)
	if err != nil {
		return nil, err
	}
	if IsSignInPage(p.URL) {
		return nil, ErrSignedOut
	}
	doc, err := p.Doc()
	if err != nil {
		return nil, err
	}
	years := ParseYears(doc)
	if len(years) == 0 {
		return nil, fmt.Errorf("no year filter found on the order page; run `amz page orders` and check the markup")
	}
	return years, nil
}

var ErrSignedOut = fmt.Errorf("not signed in; run `amz login` first")

const pageSize = 10

// ListYear walks every page of one year's orders. Progress, if not nil,
// is called after each page with the running count.
func ListYear(ctx context.Context, site Site, year int, progress func(page, total int)) ([]Order, error) {
	return ListYearSince(ctx, site, year, "", progress)
}

// ListYearSince is ListYear that stops paging once a whole page is
// older than sinceISO (orders come newest first), so an incremental
// sync only touches the first few pages.
func ListYearSince(ctx context.Context, site Site, year int, sinceISO string, progress func(page, total int)) ([]Order, error) {
	var all []Order
	seen := map[string]bool{}
	expected := 0
	for start := 0; start < 10000; start += pageSize {
		p, err := Load(ctx, site.OrdersURL(year, start), 20*time.Second, ordersReadySelector)
		if err != nil {
			return all, err
		}
		if IsSignInPage(p.URL) {
			return all, ErrSignedOut
		}
		doc, err := p.Doc()
		if err != nil {
			return all, err
		}
		if start == 0 {
			expected = ParseOrderCount(doc)
		}
		got := ParseOrders(site, doc)
		fresh, old := 0, 0
		for _, o := range got {
			if sinceISO != "" && o.DateISO != "" && o.DateISO < sinceISO {
				old++
			}
			if seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			all = append(all, o)
			fresh++
		}
		if progress != nil {
			progress(start/pageSize+1, len(all))
		}
		if fresh == 0 || len(got) < pageSize || (expected > 0 && len(all) >= expected) || (len(got) > 0 && old == len(got)) {
			break
		}
		time.Sleep(Delay)
	}
	return all, nil
}
