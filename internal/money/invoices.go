package money

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jgalea/amz/internal/store"
)

// Quarter is a year and quarter, from "2026Q3" or "2026-Q3".
type Quarter struct {
	Year int
	Q    int
}

var quarterRe = regexp.MustCompile(`^(\d{4})-?[Qq]([1-4])$`)

func ParseQuarter(s string) (Quarter, error) {
	m := quarterRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Quarter{}, fmt.Errorf("%q is not a quarter like 2026Q3", s)
	}
	y, _ := strconv.Atoi(m[1])
	q, _ := strconv.Atoi(m[2])
	return Quarter{y, q}, nil
}

func (q Quarter) Range() (string, string) {
	start := fmt.Sprintf("%d-%02d-01", q.Year, (q.Q-1)*3+1)
	endMonth := q.Q * 3
	end := fmt.Sprintf("%d-%02d-31", q.Year, endMonth)
	return start, end
}

func (q Quarter) String() string { return fmt.Sprintf("%dQ%d", q.Year, q.Q) }

// Exported is the outcome for one order.
type Exported struct {
	OrderID string   `json:"order_id"`
	Date    string   `json:"date"`
	Total   string   `json:"total"`
	Title   string   `json:"title"`
	Files   []string `json:"files,omitempty"`
	// Missing is set when no invoice PDF exists for the order; the
	// printable summary is not a VAT invoice.
	Missing bool   `json:"missing"`
	Summary string `json:"summary,omitempty"`
}

var invoiceFileRe = regexp.MustCompile(`_(\d{3}-\d{7}-\d{7})_invoice-\d+\.pdf$`)
var summaryFileRe = regexp.MustCompile(`_(\d{3}-\d{7}-\d{7})_summary\.pdf$`)

// index maps order id to invoice PDFs (and summary PDFs) found under
// the source directories.
func index(srcDirs []string) (map[string][]string, map[string]string, error) {
	inv := map[string][]string{}
	sum := map[string]string{}
	for _, dir := range srcDirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if m := invoiceFileRe.FindStringSubmatch(path); m != nil {
				inv[m[1]] = append(inv[m[1]], path)
			} else if m := summaryFileRe.FindStringSubmatch(path); m != nil {
				sum[m[1]] = path
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return inv, sum, nil
}

// Export copies the invoice PDFs of every order in the quarter into
// <root>/<year>/Q<n>/Amazon/<account>/ and reports the orders that
// have none. Existing files are left alone. With withSummary, orders
// without an invoice get their printable summary copied instead, named
// so the accountant can tell them apart.
func Export(orders []store.Order, q Quarter, srcDirs []string, root, account string, withSummary bool) ([]Exported, string, error) {
	start, end := q.Range()
	dest := filepath.Join(root, strconv.Itoa(q.Year), fmt.Sprintf("Q%d", q.Q), "Amazon", account)
	inv, sum, err := index(srcDirs)
	if err != nil {
		return nil, dest, err
	}
	var out []Exported
	for _, o := range orders {
		if o.DateISO < start || o.DateISO > end {
			continue
		}
		if o.TotalN == 0 && strings.Contains(strings.ToLower(o.Status), "cancel") {
			continue
		}
		e := Exported{OrderID: o.ID, Date: o.DateISO, Total: o.Total}
		if len(o.Items) > 0 {
			e.Title = o.Items[0].Title
		}
		files := inv[o.ID]
		if len(files) == 0 {
			e.Missing = true
			if withSummary && sum[o.ID] != "" {
				files = []string{sum[o.ID]}
			}
		}
		for _, src := range files {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return out, dest, err
			}
			name := filepath.Base(src)
			if e.Missing {
				name = strings.TrimSuffix(name, ".pdf") + "_NOT-AN-INVOICE.pdf"
			}
			target := filepath.Join(dest, name)
			if _, err := os.Stat(target); err == nil {
				e.Files = append(e.Files, target)
				continue
			}
			if err := copyFile(src, target); err != nil {
				return out, dest, err
			}
			e.Files = append(e.Files, target)
		}
		if e.Missing && sum[o.ID] != "" {
			e.Summary = sum[o.ID]
		}
		out = append(out, e)
	}
	return out, dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
