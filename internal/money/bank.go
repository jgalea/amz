package money

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/product"
	"github.com/jgalea/amz/internal/store"
)

// BankRow is one statement line that looks like Amazon.
type BankRow struct {
	Source      string  `json:"source"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	Line        int     `json:"line"`
}

var amazonDescRe = regexp.MustCompile(`(?i)amazon|amzn|amz\b`)

// Statement reads a Revolut, N26 or Wise CSV export (recognised by its
// header) and keeps the Amazon lines. Unknown layouts are read as
// date,amount,description when those columns can be found by name.
func Statement(r io.Reader) ([]BankRow, string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, "", err
	}
	if len(rows) < 2 {
		return nil, "", fmt.Errorf("statement has no rows")
	}
	head := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		head[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))
	}
	col := func(names ...string) int {
		for _, n := range names {
			for i, h := range head {
				if h == n {
					return i
				}
			}
		}
		return -1
	}
	source := "csv"
	dateCol, amtCol, descCol, curCol, feeCol := -1, -1, -1, -1, -1
	switch {
	case col("started date") >= 0 && col("completed date") >= 0:
		source = "revolut"
		dateCol, amtCol, descCol, curCol, feeCol = col("completed date"), col("amount"), col("description"), col("currency"), col("fee")
		if dateCol < 0 {
			dateCol = col("started date")
		}
	case col("payee") >= 0 && col("amount (eur)") >= 0:
		source = "n26"
		dateCol, amtCol, descCol, curCol = col("value date", "booking date", "date"), col("amount (eur)"), col("payee"), -1
	case col("partner name") >= 0 && col("amount (eur)") >= 0:
		source = "n26"
		dateCol, amtCol, descCol = col("value date", "booking date"), col("amount (eur)"), col("partner name")
	case col("transferwise id") >= 0 || col("id") >= 0 && col("merchant") >= 0:
		source = "wise"
		dateCol, amtCol, descCol, curCol = col("date", "created on"), col("amount"), col("merchant", "description"), col("currency")
		if descCol < 0 {
			descCol = col("description")
		}
	default:
		dateCol, amtCol, descCol, curCol = col("date", "booking date", "completed date"), col("amount"), col("description", "merchant", "payee", "name"), col("currency")
	}
	if dateCol < 0 || amtCol < 0 || descCol < 0 {
		return nil, source, fmt.Errorf("could not find date, amount and description columns in %v", rows[0])
	}
	var out []BankRow
	for i, r := range rows[1:] {
		if len(r) <= dateCol || len(r) <= amtCol || len(r) <= descCol {
			continue
		}
		desc := strings.TrimSpace(r[descCol])
		if !amazonDescRe.MatchString(desc) {
			continue
		}
		amt, ok := parseSigned(r[amtCol])
		if !ok {
			continue
		}
		if feeCol >= 0 && len(r) > feeCol {
			if fee, ok := parseSigned(r[feeCol]); ok && fee != 0 && amt < 0 {
				amt -= math.Abs(fee)
			}
		}
		row := BankRow{Source: source, Date: isoDay(r[dateCol]), Amount: round2(amt), Description: desc, Line: i + 2, Currency: "EUR"}
		if curCol >= 0 && len(r) > curCol && r[curCol] != "" {
			row.Currency = strings.ToUpper(strings.TrimSpace(r[curCol]))
		}
		out = append(out, row)
	}
	return out, source, nil
}

func parseSigned(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	v, ok := product.Number(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+"))
	if !ok {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

// isoDay normalises the date column: "2026-08-21 14:02:11", "21/08/2026",
// "21.08.2026" or already ISO.
func isoDay(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return amazon.ParseDate(s)
}

// Match is one bank line paired with a store transaction, or not.
type Match struct {
	Bank  BankRow            `json:"bank"`
	Tx    *store.Transaction `json:"transaction,omitempty"`
	Days  int                `json:"days_apart,omitempty"`
	State string             `json:"state"`
}

// Reconcile pairs each Amazon bank line with the store transaction of
// the same amount (opposite sign convention: a card charge is negative
// on both sides) within `window` days. Amounts and dates are compared
// exactly enough to catch double charges and refunds that never landed;
// leftovers on either side are reported.
func Reconcile(bank []BankRow, tx []store.Transaction, window int) (matches []Match, unmatchedTx []store.Transaction) {
	used := make([]bool, len(tx))
	dayOf := func(s string) (time.Time, bool) {
		t, err := time.Parse("2006-01-02", s)
		return t, err == nil
	}
	for _, b := range bank {
		bd, okb := dayOf(b.Date)
		best, bestDays := -1, window+1
		for i, t := range tx {
			if used[i] {
				continue
			}
			amt := t.AmountN
			if t.Refund && amt < 0 {
				amt = -amt
			}
			if !t.Refund && amt > 0 {
				amt = -amt
			}
			if math.Abs(amt-b.Amount) > 0.005 {
				continue
			}
			days := 0
			if td, okt := dayOf(t.DateISO); okb && okt {
				days = int(math.Abs(td.Sub(bd).Hours()) / 24)
			}
			if days < bestDays {
				best, bestDays = i, days
			}
		}
		m := Match{Bank: b, State: "unmatched: no Amazon transaction of this amount"}
		if best >= 0 {
			used[best] = true
			t := tx[best]
			m.Tx, m.Days, m.State = &t, bestDays, "matched"
		} else if b.Amount > 0 {
			m.State = "refund on the statement with no Amazon refund"
		} else {
			m.State = "charge on the statement with no Amazon charge (double charge?)"
		}
		matches = append(matches, m)
	}
	// Only Amazon transactions inside the statement's own period can be
	// missing from it.
	lo, hi := "", ""
	for _, b := range bank {
		if b.Date == "" {
			continue
		}
		if lo == "" || b.Date < lo {
			lo = b.Date
		}
		if b.Date > hi {
			hi = b.Date
		}
	}
	for i, t := range tx {
		if used[i] || strings.TrimSpace(t.Amount) == "" || t.DateISO == "" {
			continue
		}
		if lo != "" && (t.DateISO < lo || t.DateISO > hi) {
			continue
		}
		unmatchedTx = append(unmatchedTx, t)
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Bank.Date > matches[j].Bank.Date })
	return matches, unmatchedTx
}
