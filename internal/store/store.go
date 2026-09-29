// Package store is the local SQLite database: everything a sync scrapes
// lands here, and the analysis commands read from it offline.
package store

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jgalea/amz/internal/amazon"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS orders (
  account TEXT NOT NULL, market TEXT NOT NULL, id TEXT NOT NULL,
  date TEXT, date_iso TEXT, total_text TEXT, total REAL, currency TEXT,
  status TEXT, returns TEXT, return_until TEXT, delivered_iso TEXT,
  url TEXT, invoice_url TEXT, synced_at TEXT,
  PRIMARY KEY (account, market, id)
);
CREATE INDEX IF NOT EXISTS orders_date ON orders (date_iso);
CREATE TABLE IF NOT EXISTS items (
  account TEXT NOT NULL, market TEXT NOT NULL, order_id TEXT NOT NULL, seq INTEGER NOT NULL,
  asin TEXT, title TEXT, qty INTEGER, url TEXT,
  PRIMARY KEY (account, market, order_id, seq)
);
CREATE INDEX IF NOT EXISTS items_asin ON items (asin);
CREATE TABLE IF NOT EXISTS transactions (
  account TEXT NOT NULL, market TEXT NOT NULL, key TEXT NOT NULL,
  date TEXT, date_iso TEXT, amount_text TEXT, amount REAL, method TEXT,
  order_ids TEXT, merchant TEXT, status TEXT, refund INTEGER,
  PRIMARY KEY (account, market, key)
);
CREATE TABLE IF NOT EXISTS giftcard (
  account TEXT NOT NULL, market TEXT NOT NULL, key TEXT NOT NULL,
  date TEXT, date_iso TEXT, amount_text TEXT, amount REAL, description TEXT, order_id TEXT,
  PRIMARY KEY (account, market, key)
);
CREATE TABLE IF NOT EXISTS returns (
  account TEXT NOT NULL, market TEXT NOT NULL, key TEXT NOT NULL,
  order_id TEXT, title TEXT, status TEXT, lines TEXT, seen_at TEXT,
  PRIMARY KEY (account, market, key)
);
CREATE TABLE IF NOT EXISTS summaries (
  account TEXT NOT NULL, market TEXT NOT NULL, order_id TEXT NOT NULL,
  total REAL, refund_total REAL, open_return INTEGER, fetched_at TEXT,
  PRIMARY KEY (account, market, order_id)
);
CREATE TABLE IF NOT EXISTS syncs (
  account TEXT NOT NULL, market TEXT NOT NULL, what TEXT NOT NULL, at TEXT,
  PRIMARY KEY (account, market, what)
);
CREATE TABLE IF NOT EXISTS prices (
  asin TEXT NOT NULL, market TEXT NOT NULL, at TEXT NOT NULL,
  title TEXT, price REAL, currency TEXT, price_eur REAL, list_price REAL, used_price REAL,
  availability TEXT, seller TEXT, ships_from TEXT, delivery_dest TEXT, account TEXT,
  PRIMARY KEY (asin, market, at, account)
);
CREATE TABLE IF NOT EXISTS watches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  asin TEXT NOT NULL, markets TEXT, below REAL, used INTEGER, title TEXT,
  created_at TEXT, last_checked TEXT, last_price REAL, last_market TEXT, notified_at TEXT
);
CREATE TABLE IF NOT EXISTS notices (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account TEXT, message_id TEXT UNIQUE, date TEXT, sender TEXT, subject TEXT, kind TEXT, seen_at TEXT
);
`

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := private.Dir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func key(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:8])
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func fromJSON(raw string, v any) {
	if raw != "" {
		json.Unmarshal([]byte(raw), v)
	}
}

// Order is a stored order, with where it came from.
type Order struct {
	Account string `json:"account"`
	Market  string `json:"market"`
	amazon.Order
	TotalN       float64 `json:"total_n"`
	Currency     string  `json:"currency,omitempty"`
	ReturnUntil  string  `json:"return_until,omitempty"`
	DeliveredISO string  `json:"delivered_iso,omitempty"`
}

// Currency guesses the currency from the total's symbol, falling back
// to what the storefront trades in.
func Currency(total, fallback string) string {
	switch {
	case strings.Contains(total, "€") || strings.Contains(total, "EUR"):
		return "EUR"
	case strings.Contains(total, "£") || strings.Contains(total, "GBP"):
		return "GBP"
	case strings.Contains(total, "$") || strings.Contains(total, "USD"):
		return "USD"
	case strings.Contains(total, "zł") || strings.Contains(total, "PLN"):
		return "PLN"
	case strings.Contains(total, "kr") || strings.Contains(total, "SEK"):
		return "SEK"
	}
	return fallback
}

// UpsertOrders writes orders for one account and storefront. An order
// already present is replaced, since its status moves over time.
func (s *Store) UpsertOrders(account, mkt, currency string, orders []amazon.Order) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	for _, o := range orders {
		total, _ := amazon.ParseAmount(o.Total)
		_, err := tx.Exec(`INSERT INTO orders (account, market, id, date, date_iso, total_text, total, currency, status, returns, return_until, delivered_iso, url, invoice_url, synced_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(account, market, id) DO UPDATE SET date=excluded.date, date_iso=excluded.date_iso, total_text=excluded.total_text, total=excluded.total, currency=excluded.currency,
			status=excluded.status, returns=excluded.returns, return_until=excluded.return_until, delivered_iso=excluded.delivered_iso, url=excluded.url, invoice_url=excluded.invoice_url, synced_at=excluded.synced_at`,
			account, mkt, o.ID, o.Date, o.DateISO, o.Total, total, Currency(o.Total, currency), o.Status, toJSON(o.Returns), o.ReturnUntil, o.DeliveredISO, o.URL, o.InvoiceURL, at)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM items WHERE account=? AND market=? AND order_id=?`, account, mkt, o.ID); err != nil {
			return err
		}
		for i, it := range o.Items {
			qty, _ := strconv.Atoi(it.Qty)
			if qty == 0 {
				qty = 1
			}
			if _, err := tx.Exec(`INSERT INTO items (account, market, order_id, seq, asin, title, qty, url) VALUES (?,?,?,?,?,?,?,?)`,
				account, mkt, o.ID, i, it.ASIN, it.Title, qty, it.URL); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Query narrows what the read side returns. Empty fields mean "any".
type Query struct {
	Accounts []string
	Markets  []string
	Since    string
	Until    string
	Grep     string
	ASIN     string
	OrderID  string
}

func (q Query) where(prefix string) (string, []any) {
	var conds []string
	var args []any
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
		conds = append(conds, fmt.Sprintf("%s%s IN (%s)", prefix, col, ph))
		for _, v := range vals {
			args = append(args, v)
		}
	}
	in("account", q.Accounts)
	in("market", q.Markets)
	if q.Since != "" {
		conds = append(conds, prefix+"date_iso >= ?")
		args = append(args, q.Since)
	}
	if q.Until != "" {
		conds = append(conds, prefix+"date_iso <= ?")
		args = append(args, q.Until)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Orders returns stored orders, newest first, with their items.
func (s *Store) Orders(q Query) ([]Order, error) {
	where, args := q.where("o.")
	if q.OrderID != "" {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += "o.id = ?"
		args = append(args, q.OrderID)
	}
	if q.ASIN != "" || q.Grep != "" {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += "EXISTS (SELECT 1 FROM items i WHERE i.account=o.account AND i.market=o.market AND i.order_id=o.id"
		if q.ASIN != "" {
			where += " AND i.asin = ?"
			args = append(args, strings.ToUpper(q.ASIN))
		}
		if q.Grep != "" {
			where += " AND lower(i.title) LIKE ?"
			args = append(args, "%"+strings.ToLower(q.Grep)+"%")
		}
		where += ")"
	}
	rows, err := s.db.Query(`SELECT o.account, o.market, o.id, o.date, o.date_iso, o.total_text, o.total, o.currency, o.status, o.returns, o.return_until, o.delivered_iso, o.url, o.invoice_url
		FROM orders o`+where+` ORDER BY o.date_iso DESC, o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	index := map[string]int{}
	for rows.Next() {
		var o Order
		var returns string
		if err := rows.Scan(&o.Account, &o.Market, &o.ID, &o.Date, &o.DateISO, &o.Total, &o.TotalN, &o.Currency, &o.Status, &returns, &o.ReturnUntil, &o.DeliveredISO, &o.URL, &o.InvoiceURL); err != nil {
			return nil, err
		}
		fromJSON(returns, &o.Returns)
		o.Items = []amazon.Item{}
		index[o.Account+"|"+o.Market+"|"+o.ID] = len(out)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	irows, err := s.db.Query(`SELECT account, market, order_id, asin, title, qty, url FROM items ORDER BY account, market, order_id, seq`)
	if err != nil {
		return nil, err
	}
	defer irows.Close()
	for irows.Next() {
		var acc, mkt, id string
		var it amazon.Item
		var qty int
		if err := irows.Scan(&acc, &mkt, &id, &it.ASIN, &it.Title, &qty, &it.URL); err != nil {
			return nil, err
		}
		it.Qty = strconv.Itoa(qty)
		if i, ok := index[acc+"|"+mkt+"|"+id]; ok {
			out[i].Items = append(out[i].Items, it)
		}
	}
	return out, irows.Err()
}

// Transaction is a stored payments row.
type Transaction struct {
	Account string `json:"account"`
	Market  string `json:"market"`
	amazon.Transaction
	AmountN float64 `json:"amount_n"`
}

func txKey(t amazon.Transaction, n int) string {
	return key(t.DateISO, t.Amount, t.Method, strings.Join(t.OrderIDs, ","), t.OrderID, t.Status, t.Merchant, strconv.Itoa(n))
}

// UpsertTransactions stores payments rows. Two identical rows on the
// same day are two real transactions, so the key carries a counter.
func (s *Store) UpsertTransactions(account, mkt string, list []amazon.Transaction) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	dup := map[string]int{}
	added := 0
	for _, t := range list {
		base := txKey(t, 0)
		n := dup[base]
		dup[base]++
		k := txKey(t, n)
		amt, _ := amazon.ParseAmount(t.Amount)
		if t.Refund && amt < 0 {
			amt = -amt
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO transactions (account, market, key, date, date_iso, amount_text, amount, method, order_ids, merchant, status, refund)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, account, mkt, k, t.Date, t.DateISO, t.Amount, amt, t.Method, toJSON(orderIDs(t)), t.Merchant, t.Status, t.Refund)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

func orderIDs(t amazon.Transaction) []string {
	if len(t.OrderIDs) > 0 {
		return t.OrderIDs
	}
	if t.OrderID != "" {
		return []string{t.OrderID}
	}
	return nil
}

func (s *Store) Transactions(q Query) ([]Transaction, error) {
	where, args := q.where("")
	rows, err := s.db.Query(`SELECT account, market, date, date_iso, amount_text, amount, method, order_ids, merchant, status, refund FROM transactions`+where+` ORDER BY date_iso DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		var ids string
		if err := rows.Scan(&t.Account, &t.Market, &t.Date, &t.DateISO, &t.Amount, &t.AmountN, &t.Method, &ids, &t.Merchant, &t.Status, &t.Refund); err != nil {
			return nil, err
		}
		fromJSON(ids, &t.OrderIDs)
		if len(t.OrderIDs) > 0 {
			t.OrderID = t.OrderIDs[0]
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type GiftCardEntry struct {
	Account string `json:"account"`
	Market  string `json:"market"`
	amazon.GiftCardEntry
	AmountN float64 `json:"amount_n"`
}

func (s *Store) UpsertGiftCard(account, mkt string, list []amazon.GiftCardEntry) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	dup := map[string]int{}
	added := 0
	for _, g := range list {
		base := key(g.DateISO, g.Amount, g.Description, g.OrderID)
		n := dup[base]
		dup[base]++
		amt, _ := amazon.ParseAmount(g.Amount)
		res, err := tx.Exec(`INSERT OR IGNORE INTO giftcard (account, market, key, date, date_iso, amount_text, amount, description, order_id) VALUES (?,?,?,?,?,?,?,?,?)`,
			account, mkt, key(base, strconv.Itoa(n)), g.Date, g.DateISO, g.Amount, amt, g.Description, g.OrderID)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

func (s *Store) GiftCard(q Query) ([]GiftCardEntry, error) {
	where, args := q.where("")
	rows, err := s.db.Query(`SELECT account, market, date, date_iso, amount_text, amount, description, order_id FROM giftcard`+where+` ORDER BY date_iso DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GiftCardEntry
	for rows.Next() {
		var g GiftCardEntry
		if err := rows.Scan(&g.Account, &g.Market, &g.Date, &g.DateISO, &g.Amount, &g.AmountN, &g.Description, &g.OrderID); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type Return struct {
	Account string `json:"account"`
	Market  string `json:"market"`
	amazon.Return
	SeenAt string `json:"seen_at,omitempty"`
}

func (s *Store) UpsertReturns(account, mkt string, list []amazon.Return) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	for _, r := range list {
		k := key(r.OrderID, r.Title)
		_, err := tx.Exec(`INSERT INTO returns (account, market, key, order_id, title, status, lines, seen_at) VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(account, market, key) DO UPDATE SET status=excluded.status, lines=excluded.lines, seen_at=excluded.seen_at`,
			account, mkt, k, r.OrderID, r.Title, r.Status, toJSON(r.Lines), at)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Returns(q Query) ([]Return, error) {
	where, args := Query{Accounts: q.Accounts, Markets: q.Markets}.where("")
	rows, err := s.db.Query(`SELECT account, market, order_id, title, status, lines, seen_at FROM returns`+where+` ORDER BY seen_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Return
	for rows.Next() {
		var r Return
		var lines string
		if err := rows.Scan(&r.Account, &r.Market, &r.OrderID, &r.Title, &r.Status, &lines, &r.SeenAt); err != nil {
			return nil, err
		}
		fromJSON(lines, &r.Lines)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertSummaries(account, mkt string, list map[string]amazon.Summary) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	for id, sum := range list {
		_, err := tx.Exec(`INSERT INTO summaries (account, market, order_id, total, refund_total, open_return, fetched_at) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(account, market, order_id) DO UPDATE SET total=excluded.total, refund_total=excluded.refund_total, open_return=excluded.open_return, fetched_at=excluded.fetched_at`,
			account, mkt, id, sum.Total, sum.RefundTotal, sum.OpenReturn, at)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Summaries returns the saved order-page figures for one account and
// storefront, keyed by order id.
func (s *Store) Summaries(account, mkt string) (map[string]amazon.Summary, error) {
	rows, err := s.db.Query(`SELECT order_id, total, refund_total, open_return FROM summaries WHERE account=? AND market=?`, account, mkt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]amazon.Summary{}
	for rows.Next() {
		var sum amazon.Summary
		if err := rows.Scan(&sum.OrderID, &sum.Total, &sum.RefundTotal, &sum.OpenReturn); err != nil {
			return nil, err
		}
		out[sum.OrderID] = sum
	}
	return out, rows.Err()
}

func (s *Store) SetSync(account, mkt, what string) error {
	_, err := s.db.Exec(`INSERT INTO syncs (account, market, what, at) VALUES (?,?,?,?) ON CONFLICT(account, market, what) DO UPDATE SET at=excluded.at`, account, mkt, what, now())
	return err
}

// LastSync returns when the given thing was last synced, or zero.
func (s *Store) LastSync(account, mkt, what string) time.Time {
	var at string
	if err := s.db.QueryRow(`SELECT at FROM syncs WHERE account=? AND market=? AND what=?`, account, mkt, what).Scan(&at); err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, at)
	return t
}

// Syncs lists every sync stamp, for `amz status`.
func (s *Store) Syncs() (map[string]time.Time, error) {
	rows, err := s.db.Query(`SELECT account, market, what, at FROM syncs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var acc, mkt, what, at string
		if err := rows.Scan(&acc, &mkt, &what, &at); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339, at)
		out[acc+"/"+mkt+"/"+what] = t
	}
	return out, rows.Err()
}

// Counts is a quick census of the store, per account and storefront.
type Counts struct {
	Account      string `json:"account"`
	Market       string `json:"market"`
	Orders       int    `json:"orders"`
	Transactions int    `json:"transactions"`
	GiftCard     int    `json:"giftcard"`
	Returns      int    `json:"returns"`
	Summaries    int    `json:"summaries"`
	Oldest       string `json:"oldest_order,omitempty"`
	Newest       string `json:"newest_order,omitempty"`
}

func (s *Store) Census() ([]Counts, error) {
	rows, err := s.db.Query(`SELECT account, market, count(*), min(date_iso), max(date_iso) FROM orders GROUP BY account, market ORDER BY account, market`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Counts
	for rows.Next() {
		var c Counts
		var oldest, newest sql.NullString
		if err := rows.Scan(&c.Account, &c.Market, &c.Orders, &oldest, &newest); err != nil {
			return nil, err
		}
		c.Oldest, c.Newest = oldest.String, newest.String
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		c := &out[i]
		s.db.QueryRow(`SELECT count(*) FROM transactions WHERE account=? AND market=?`, c.Account, c.Market).Scan(&c.Transactions)
		s.db.QueryRow(`SELECT count(*) FROM giftcard WHERE account=? AND market=?`, c.Account, c.Market).Scan(&c.GiftCard)
		s.db.QueryRow(`SELECT count(*) FROM returns WHERE account=? AND market=?`, c.Account, c.Market).Scan(&c.Returns)
		s.db.QueryRow(`SELECT count(*) FROM summaries WHERE account=? AND market=?`, c.Account, c.Market).Scan(&c.Summaries)
	}
	return out, nil
}
