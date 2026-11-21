package deal

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/product"
)

type Point struct {
	Price float64 `json:"price"`
	Date  string  `json:"date"`
}

type Row struct {
	Series   string `json:"series"`
	Currency string `json:"currency"`
	Lowest   Point  `json:"lowest"`
	Highest  Point  `json:"highest"`
	Current  Point  `json:"current"`
}

var legendCell = regexp.MustCompile(`([€£$])\s?([\d.,]+)\s*\(\s*([A-Z][a-z]{2})\s+(\d{1,2}),\s*(\d{4})\s*\)`)

func parseCell(m []string) (Point, bool) {
	v, ok := product.Number(m[2])
	if !ok || v <= 0 {
		return Point{}, false
	}
	d, err := time.Parse("Jan 2 2006", fmt.Sprintf("%s %s %s", m[3], m[4], m[5]))
	if err != nil {
		return Point{}, false
	}
	return Point{Price: v, Date: d.Format("2006-01-02")}, true
}

func currencyOfSymbol(s string) string {
	switch s {
	case "€":
		return "EUR"
	case "£":
		return "GBP"
	}
	return "USD"
}

// ParseLegend reads the rows of Camel's chart legend from OCR text. A row
// needs three dated prices (lowest, highest, current) in a consistent
// order; anything else is dropped. The second return value says why
// nothing was read.
func ParseLegend(text string) ([]Row, string) {
	var rows []Row
	for _, line := range strings.Split(text, "\n") {
		cells := legendCell.FindAllStringSubmatch(line, -1)
		if len(cells) != 3 {
			continue
		}
		l := strings.ToLower(line)
		var series string
		switch {
		case strings.Contains(l, "amazon"):
			series = "amazon"
		case strings.Contains(l, "used"):
			series = "used"
		case strings.Contains(l, "new") || strings.Contains(l, "party"):
			series = "new"
		default:
			continue
		}
		var pts [3]Point
		ok := true
		for i, c := range cells {
			if pts[i], ok = parseCell(c); !ok {
				break
			}
		}
		if !ok || pts[0].Price > pts[1].Price || pts[0].Price > pts[2].Price || pts[2].Price > pts[1].Price {
			continue
		}
		rows = append(rows, Row{Series: series, Currency: currencyOfSymbol(cells[0][1]), Lowest: pts[0], Highest: pts[1], Current: pts[2]})
	}
	if len(rows) == 0 {
		return nil, "no legend row with lowest, highest and current prices could be read from the chart"
	}
	return rows, ""
}

// OCR runs tesseract on a PNG and returns the text.
func OCR(path string) (string, error) {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		return "", fmt.Errorf("tesseract is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, path, "-", "--psm", "6").Output()
	if err != nil {
		return "", fmt.Errorf("tesseract failed: %v", err)
	}
	return string(out), nil
}

// ReadLegend OCRs a Camel chart and parses its legend.
func ReadLegend(path string) ([]Row, string) {
	text, err := OCR(path)
	if err != nil {
		return nil, err.Error()
	}
	return ParseLegend(text)
}
