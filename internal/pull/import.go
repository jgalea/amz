package pull

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jgalea/amz/internal/amazon"
	"github.com/jgalea/amz/internal/market"
	"github.com/jgalea/amz/internal/store"
)

// Dump is the file layout `amz dump` (and amz before it) writes:
// orders.json, refunds.json, giftcard.json and invoices/*_summary.html.
type Dump struct {
	Orders       []amazon.Order
	Transactions []amazon.Transaction
	Returns      []amazon.Return
	GiftCard     []amazon.GiftCardEntry
	Summaries    map[string]amazon.Summary
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// ReadDump loads a dump directory. Missing optional files are skipped;
// orders.json must exist.
func ReadDump(dir string) (Dump, error) {
	var d Dump
	if err := readJSON(filepath.Join(dir, "orders.json"), &d.Orders); err != nil {
		return d, err
	}
	var rep struct {
		Transactions []amazon.Transaction `json:"transactions"`
		Returns      []amazon.Return      `json:"returns"`
	}
	refunds := filepath.Join(dir, "refunds.json")
	if err := readJSON(refunds, &rep); err != nil {
		if os.IsNotExist(err) {
			rep.Transactions = nil
		} else if err2 := readJSON(refunds, &rep.Transactions); err2 != nil {
			return d, err
		}
	}
	d.Transactions, d.Returns = rep.Transactions, rep.Returns
	if err := readJSON(filepath.Join(dir, "giftcard.json"), &d.GiftCard); err != nil && !os.IsNotExist(err) {
		return d, err
	}
	sums, err := amazon.LoadSummaries(filepath.Join(dir, "invoices"))
	if err != nil {
		return d, err
	}
	d.Summaries = sums
	return d, nil
}

// Import writes a dump into the store under one account and storefront.
func Import(db *store.Store, account string, sf market.Storefront, d Dump) (Result, error) {
	res := Result{Account: account, Market: sf.Code}
	if err := db.UpsertOrders(account, sf.Code, sf.Currency, d.Orders); err != nil {
		return res, err
	}
	res.Orders = len(d.Orders)
	n, err := db.UpsertTransactions(account, sf.Code, d.Transactions)
	if err != nil {
		return res, err
	}
	res.Transactions = n
	if err := db.UpsertReturns(account, sf.Code, d.Returns); err != nil {
		return res, err
	}
	res.Returns = len(d.Returns)
	n, err = db.UpsertGiftCard(account, sf.Code, d.GiftCard)
	if err != nil {
		return res, err
	}
	res.GiftCard = n
	if err := db.UpsertSummaries(account, sf.Code, d.Summaries); err != nil {
		return res, err
	}
	res.Summaries = len(d.Summaries)
	for _, what := range []string{"orders", "returns", "transactions", "giftcard"} {
		if err := db.SetSync(account, sf.Code, what); err != nil {
			return res, err
		}
	}
	return res, nil
}
