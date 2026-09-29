package after

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/store"
)

// ChaseLine is a return that Amazon has had for a while with no refund
// to show for it.
type ChaseLine struct {
	Account    string   `json:"account"`
	Market     string   `json:"market"`
	OrderID    string   `json:"order_id"`
	OrderDate  string   `json:"order_date"`
	Title      string   `json:"title"`
	Total      string   `json:"total"`
	TotalN     float64  `json:"total_n"`
	ReturnDate string   `json:"return_date,omitempty"`
	DaysOpen   int      `json:"days_open"`
	Status     string   `json:"status,omitempty"`
	RMA        string   `json:"rma,omitempty"`
	Tracking   string   `json:"tracking,omitempty"`
	Refunded   float64  `json:"refunded"`
	Lines      []string `json:"lines,omitempty"`
	URL        string   `json:"url"`
}

var (
	rmaRe          = regexp.MustCompile(`(?i)RMA[^A-Za-z0-9]{0,6}([A-Za-z0-9]{6,})`)
	trackingRe     = regexp.MustCompile(`(?i)(?:tracking|seguimiento|suivi|sendungs|tracciamento)[^A-Za-z0-9]{0,12}([A-Z0-9]{8,})`)
	privateRe      = regexp.MustCompile(`(?i)^(?:phone|tel|teléfono|telefon|returned from|devuelto por|address|dirección)`)
	addressStartRe = regexp.MustCompile(`(?i)^(?:returned from|devuelto por|devuelto desde|retour de|zurückgesandt von|restituito da|devolvido por|returned by)`)
)

// isLabel is a returns-page heading: mostly capitals, no digits.
func isLabel(line string) bool {
	upper, letters := 0, 0
	for _, r := range line {
		if r >= '0' && r <= '9' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			upper++
			letters++
		} else if r >= 'a' && r <= 'z' {
			letters++
		}
	}
	return letters >= 4 && upper*10 >= letters*8
}

// Chase matches the returns centre entries against the money that came
// back and keeps those older than minDays with nothing refunded. The
// refund figure comes from the same ledger the audit uses.
func Chase(orders []store.Order, returns []store.Return, refunded map[string]float64, summaries map[string]amazon.Summary, minDays int, today time.Time) []ChaseLine {
	byID := map[string]store.Order{}
	for _, o := range orders {
		byID[o.ID] = o
	}
	var out []ChaseLine
	seen := map[string]bool{}
	for _, r := range returns {
		if seen[r.OrderID] {
			continue
		}
		o, ok := byID[r.OrderID]
		if !ok {
			continue
		}
		got := refunded[r.OrderID]
		if s, ok := summaries[r.OrderID]; ok && s.RefundTotal > got {
			got = s.RefundTotal
		}
		if got > 0 {
			continue
		}
		l := ChaseLine{Account: r.Account, Market: r.Market, OrderID: r.OrderID, OrderDate: o.DateISO, Title: r.Title, Total: o.Total, Status: r.Status, URL: o.URL, Refunded: got}
		if l.Title == "" && len(o.Items) > 0 {
			l.Title = o.Items[0].Title
		}
		l.TotalN, _ = amazon.ParseAmount(o.Total)
		// After "returned from" the page prints the sender's address block
		// (name, street, town, phone); skip it until the next label.
		inAddress := false
		for _, line := range r.Lines {
			if addressStartRe.MatchString(line) {
				inAddress = true
				continue
			}
			if inAddress && !isLabel(line) && amazon.ParseDate(line) == "" && !rmaRe.MatchString(line) {
				continue
			}
			inAddress = false
			if d := amazon.ParseDate(line); d != "" && l.ReturnDate == "" {
				l.ReturnDate = d
			}
			if m := rmaRe.FindStringSubmatch(line); m != nil && l.RMA == "" {
				l.RMA = m[1]
			}
			if m := trackingRe.FindStringSubmatch(line); m != nil && l.Tracking == "" {
				l.Tracking = m[1]
			}
			// The returns page repeats the sender's address and phone;
			// they have no place in a claim draft.
			if !privateRe.MatchString(line) && !looksPersonal(line) {
				l.Lines = append(l.Lines, line)
			}
		}
		base := l.ReturnDate
		if base == "" {
			base = r.SeenAt[:min(10, len(r.SeenAt))]
		}
		if t, err := time.Parse("2006-01-02", base); err == nil {
			l.DaysOpen = int(today.Sub(t).Hours() / 24)
		}
		if l.DaysOpen < minDays {
			continue
		}
		seen[r.OrderID] = true
		out = append(out, l)
	}
	return out
}

// looksPersonal drops lines that read as a street address or phone
// number: mostly digits, or a comma-separated locality with a postcode.
func looksPersonal(line string) bool {
	digits := 0
	for _, r := range line {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits >= 7 && digits*2 > len(line) {
		return true
	}
	return regexp.MustCompile(`\b\d{4,5}(?:-\d{3})?\b`).MatchString(line) && strings.Contains(line, ",")
}

// Draft writes the claim text for one line: the facts, in order, that
// Amazon's support needs to find and act on the return. It is a draft
// to paste, never sent by amz.
func Draft(l ChaseLine, today time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject: Refund outstanding for return on order %s\n\n", l.OrderID)
	fmt.Fprintf(&b, "Hello,\n\nI returned an item from order %s (placed %s) and the refund has not been issued.\n\n", l.OrderID, l.OrderDate)
	fmt.Fprintf(&b, "Item: %s\n", l.Title)
	fmt.Fprintf(&b, "Order total: %s\n", l.Total)
	if l.ReturnDate != "" {
		fmt.Fprintf(&b, "Return created: %s (%d days ago)\n", l.ReturnDate, l.DaysOpen)
	}
	if l.RMA != "" {
		fmt.Fprintf(&b, "RMA: %s\n", l.RMA)
	}
	if l.Tracking != "" {
		fmt.Fprintf(&b, "Return tracking: %s\n", l.Tracking)
	}
	if l.Status != "" {
		fmt.Fprintf(&b, "Status shown in the returns centre: %s\n", l.Status)
	}
	fmt.Fprintf(&b, "Refund received to date: %.2f\n\n", l.Refunded)
	fmt.Fprintf(&b, "Please confirm receipt of the return and issue the refund of %s to the original payment method, or tell me what is holding it up.\n\n", l.Total)
	fmt.Fprintf(&b, "Order page: %s\n", l.URL)
	fmt.Fprintf(&b, "\nDrafted %s.\n", today.Format("2006-01-02"))
	return b.String()
}
