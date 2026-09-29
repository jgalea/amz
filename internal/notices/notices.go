// Package notices finds the mails from Amazon that matter for an
// account's standing: account protection, restriction, orders cancelled
// for policy reasons, refund refusals. It reads a mailbox over IMAP
// with credentials the user supplies, or .eml files from a directory.
package notices

import (
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/jgalea/amz/internal/config"
	"github.com/jgalea/amz/internal/store"
)

type Notice = store.Notice

// kinds are matched against the subject, in order; the first hit wins.
var kinds = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"restriction", regexp.MustCompile(`(?i)account (?:has been )?(?:restricted|suspended|closed|blocked|locked|on hold)|cuenta (?:ha sido )?(?:restringida|suspendida|cerrada|bloqueada)|compte (?:a été )?(?:restreint|suspendu|fermé|bloqué)|konto (?:wurde )?(?:eingeschränkt|gesperrt|geschlossen)|account (?:è stato )?(?:limitato|sospeso|chiuso)|conta (?:foi )?(?:restringida|suspensa|encerrada)`)},
	{"protection", regexp.MustCompile(`(?i)account protection|protección de (?:tu |la )?cuenta|protection de (?:votre )?compte|kontoschutz|protezione dell'account|proteção da conta|unusual activity|actividad inusual|verify your (?:identity|account)|verifica tu (?:identidad|cuenta)`)},
	{"returns-warning", regexp.MustCompile(`(?i)(?:your |about your )?return(?:s)? (?:activity|history|behaviou?r|rate)|(?:tus |las )?devoluciones|concerning (?:your )?returns|retours? (?:récents|fréquents)|rücksendungen|resi frequenti|refund (?:request )?(?:declined|denied|refused)|reembolso (?:denegado|rechazado)`)},
	{"cancelled-policy", regexp.MustCompile(`(?i)order (?:has been )?cancell?ed|(?:we|amazon) (?:have |has )?cancell?ed (?:your )?order|pedido (?:ha sido )?cancelado|commande (?:a été )?annulée|bestellung (?:wurde )?storniert|ordine (?:è stato )?annullato|encomenda (?:foi )?cancelada`)},
	{"abuse", regexp.MustCompile(`(?i)abuse|policy violation|violación de (?:la )?política|violation de (?:la )?politique|richtlinienverst|violazione della politica|violação da política`)},
}

var (
	amazonFromRe    = regexp.MustCompile(`(?i)@(?:[a-z0-9.-]+\.)?amazon\.[a-z.]+$|@amazon\.com$`)
	cancelledByMeRe = regexp.MustCompile(`(?i)(?:you|has been) (?:have )?(?:successfully )?cancell?ed|as (?:you )?requested|a petición tuya|has cancelado|que has solicitado|comme demandé|vous avez annulé|wie gewünscht|du hast storniert|come richiesto|hai annullato`)
)

// Classify says whether a mail is an Amazon notice worth keeping and
// what kind. Cancellations the customer asked for are not notices.
func Classify(from, subject, snippet string) (string, bool) {
	if !amazonFromRe.MatchString(strings.TrimSpace(strings.ToLower(from))) {
		return "", false
	}
	for _, k := range kinds {
		if k.re.MatchString(subject) {
			if k.kind == "cancelled-policy" && cancelledByMeRe.MatchString(subject+" "+snippet) {
				return "", false
			}
			return k.kind, true
		}
	}
	return "", false
}

func decodeHeader(s string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// FromFile reads one .eml (RFC 822) file.
func FromFile(path string) (Notice, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Notice{}, false, err
	}
	defer f.Close()
	msg, err := mail.ReadMessage(f)
	if err != nil {
		return Notice{}, false, fmt.Errorf("%s: %w", path, err)
	}
	from := decodeHeader(msg.Header.Get("From"))
	subject := decodeHeader(msg.Header.Get("Subject"))
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 4000))
	kind, ok := Classify(addressOf(from), subject, string(body))
	if !ok {
		return Notice{}, false, nil
	}
	n := Notice{MessageID: strings.Trim(msg.Header.Get("Message-ID"), "<> "), Sender: from, Subject: subject, Kind: kind}
	if n.MessageID == "" {
		n.MessageID = filepath.Base(path)
	}
	if t, err := mail.ParseDate(msg.Header.Get("Date")); err == nil {
		n.Date = t.UTC().Format("2006-01-02")
	}
	return n, true, nil
}

func addressOf(from string) string {
	if a, err := mail.ParseAddress(from); err == nil {
		return a.Address
	}
	return from
}

// FromDir reads every .eml under dir.
func FromDir(dir string) ([]Notice, int, error) {
	var out []Notice
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".eml") {
			return nil
		}
		scanned++
		n, ok, err := FromFile(path)
		if err != nil {
			return err
		}
		if ok {
			out = append(out, n)
		}
		return nil
	})
	return out, scanned, err
}

// FromIMAP scans a mailbox for Amazon mail since a date. The password
// comes from the environment variable named in the settings, never
// from the file.
func FromIMAP(m config.Mail, since time.Time, log func(string)) ([]Notice, int, error) {
	if m.Host == "" || m.User == "" || m.PasswordEnv == "" {
		return nil, 0, fmt.Errorf("mail is not configured: set mail.host, mail.user and mail.password_env in %s", filepath.Join(config.Dir(), "config.json"))
	}
	pass := os.Getenv(m.PasswordEnv)
	if pass == "" {
		return nil, 0, fmt.Errorf("%s is not set", m.PasswordEnv)
	}
	port := m.Port
	if port == 0 {
		port = 993
	}
	c, err := imapclient.DialTLS(fmt.Sprintf("%s:%d", m.Host, port), nil)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	if err := c.Login(m.User, pass).Wait(); err != nil {
		return nil, 0, fmt.Errorf("login: %w", err)
	}
	defer c.Logout().Wait()
	folder := m.Folder
	if folder == "" {
		folder = "INBOX"
	}
	if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, 0, fmt.Errorf("select %s: %w", folder, err)
	}
	crit := &imap.SearchCriteria{Since: since, Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: "amazon"}}}
	data, err := c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, 0, fmt.Errorf("search: %w", err)
	}
	uids := data.AllUIDs()
	if log != nil {
		log(fmt.Sprintf("%d messages from amazon in %s since %s", len(uids), folder, since.Format("2006-01-02")))
	}
	if len(uids) == 0 {
		return nil, 0, nil
	}
	var set imap.UIDSet
	set.AddNum(uids...)
	msgs, err := c.Fetch(set, &imap.FetchOptions{Envelope: true, UID: true}).Collect()
	if err != nil {
		return nil, 0, fmt.Errorf("fetch: %w", err)
	}
	var out []Notice
	for _, msg := range msgs {
		if msg.Envelope == nil {
			continue
		}
		from := ""
		if len(msg.Envelope.From) > 0 {
			from = msg.Envelope.From[0].Addr()
		}
		kind, ok := Classify(from, msg.Envelope.Subject, "")
		if !ok {
			continue
		}
		out = append(out, Notice{MessageID: msg.Envelope.MessageID, Date: msg.Envelope.Date.UTC().Format("2006-01-02"), Sender: from, Subject: msg.Envelope.Subject, Kind: kind})
	}
	return out, len(uids), nil
}
