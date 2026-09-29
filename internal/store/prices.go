package store

import (
	"database/sql"
	"strings"
	"time"
)

// Price is one observation of an ASIN on one storefront. Account is ""
// for the anonymous price and the account name for a signed-in one.
type Price struct {
	ASIN         string  `json:"asin"`
	Market       string  `json:"market"`
	At           string  `json:"at"`
	Account      string  `json:"account,omitempty"`
	Title        string  `json:"title,omitempty"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency,omitempty"`
	PriceEUR     float64 `json:"price_eur"`
	ListPrice    float64 `json:"list_price,omitempty"`
	UsedPrice    float64 `json:"used_price,omitempty"`
	Availability string  `json:"availability,omitempty"`
	Seller       string  `json:"seller,omitempty"`
	ShipsFrom    string  `json:"ships_from,omitempty"`
	DeliveryDest string  `json:"delivery_dest,omitempty"`
}

func (s *Store) AddPrice(p Price) error {
	if p.At == "" {
		p.At = now()
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO prices (asin, market, at, account, title, price, currency, price_eur, list_price, used_price, availability, seller, ships_from, delivery_dest)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		strings.ToUpper(p.ASIN), p.Market, p.At, p.Account, p.Title, p.Price, p.Currency, p.PriceEUR, p.ListPrice, p.UsedPrice, p.Availability, p.Seller, p.ShipsFrom, p.DeliveryDest)
	return err
}

// Prices returns every observation for an ASIN, newest first.
func (s *Store) Prices(asin string, since time.Time) ([]Price, error) {
	rows, err := s.db.Query(`SELECT asin, market, at, account, title, price, currency, price_eur, list_price, used_price, availability, seller, ships_from, delivery_dest
		FROM prices WHERE asin=? AND at >= ? ORDER BY at DESC`, strings.ToUpper(asin), since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Price
	for rows.Next() {
		var p Price
		if err := rows.Scan(&p.ASIN, &p.Market, &p.At, &p.Account, &p.Title, &p.Price, &p.Currency, &p.PriceEUR, &p.ListPrice, &p.UsedPrice, &p.Availability, &p.Seller, &p.ShipsFrom, &p.DeliveryDest); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type Watch struct {
	ID          int64   `json:"id"`
	ASIN        string  `json:"asin"`
	Markets     string  `json:"markets"`
	Below       float64 `json:"below"`
	Used        bool    `json:"used"`
	Title       string  `json:"title,omitempty"`
	CreatedAt   string  `json:"created_at"`
	LastChecked string  `json:"last_checked,omitempty"`
	LastPrice   float64 `json:"last_price,omitempty"`
	LastMarket  string  `json:"last_market,omitempty"`
	NotifiedAt  string  `json:"notified_at,omitempty"`
}

func (s *Store) AddWatch(w Watch) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO watches (asin, markets, below, used, title, created_at) VALUES (?,?,?,?,?,?)`,
		strings.ToUpper(w.ASIN), w.Markets, w.Below, w.Used, w.Title, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Watches() ([]Watch, error) {
	rows, err := s.db.Query(`SELECT id, asin, markets, below, used, title, created_at, last_checked, last_price, last_market, notified_at FROM watches ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Watch
	for rows.Next() {
		var w Watch
		var title, checked, mkt, notified sql.NullString
		var last sql.NullFloat64
		if err := rows.Scan(&w.ID, &w.ASIN, &w.Markets, &w.Below, &w.Used, &title, &w.CreatedAt, &checked, &last, &mkt, &notified); err != nil {
			return nil, err
		}
		w.Title, w.LastChecked, w.LastPrice, w.LastMarket, w.NotifiedAt = title.String, checked.String, last.Float64, mkt.String, notified.String
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) RemoveWatch(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM watches WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TouchWatch records the outcome of a check; notified marks that an
// alert went out, so the same drop is not re-announced every run.
func (s *Store) TouchWatch(id int64, price float64, mkt, title string, notified bool) error {
	at := now()
	var err error
	if notified {
		_, err = s.db.Exec(`UPDATE watches SET last_checked=?, last_price=?, last_market=?, title=COALESCE(NULLIF(?, ''), title), notified_at=? WHERE id=?`, at, price, mkt, title, at, id)
	} else {
		_, err = s.db.Exec(`UPDATE watches SET last_checked=?, last_price=?, last_market=?, title=COALESCE(NULLIF(?, ''), title) WHERE id=?`, at, price, mkt, title, id)
	}
	return err
}

type Notice struct {
	Account   string `json:"account,omitempty"`
	MessageID string `json:"message_id"`
	Date      string `json:"date"`
	Sender    string `json:"sender"`
	Subject   string `json:"subject"`
	Kind      string `json:"kind"`
}

// AddNotice stores a mail notice; a repeat of the same message is
// ignored, and the result says whether it was new.
func (s *Store) AddNotice(n Notice) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO notices (account, message_id, date, sender, subject, kind, seen_at) VALUES (?,?,?,?,?,?,?)`,
		n.Account, n.MessageID, n.Date, n.Sender, n.Subject, n.Kind, now())
	if err != nil {
		return false, err
	}
	c, _ := res.RowsAffected()
	return c > 0, nil
}

func (s *Store) Notices() ([]Notice, error) {
	rows, err := s.db.Query(`SELECT account, message_id, date, sender, subject, kind FROM notices ORDER BY date DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notice
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.Account, &n.MessageID, &n.Date, &n.Sender, &n.Subject, &n.Kind); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
