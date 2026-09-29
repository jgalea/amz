// Package notify sends short alerts: a macOS notification banner, an
// ntfy topic, a Telegram chat. Which ones fire comes from the settings
// file; the tokens come from the environment, never from the file.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jgalea/amz/internal/config"
)

type Result struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

var client = &http.Client{Timeout: 15 * time.Second}

// Send delivers title and body to every configured channel and reports
// per channel. A channel that is not configured is skipped, not failed.
func Send(s config.Notify, title, body string) []Result {
	var out []Result
	if s.MacOS && runtime.GOOS == "darwin" {
		out = append(out, run("macos", macOS(title, body)))
	}
	if s.Ntfy.Topic != "" {
		out = append(out, run("ntfy", ntfy(s, title, body)))
	}
	if s.Telegram.ChatID != "" && s.Telegram.TokenEnv != "" {
		out = append(out, run("telegram", telegram(s, title, body)))
	}
	return out
}

func run(channel string, err error) Result {
	r := Result{Channel: channel, OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

func macOS(title, body string) error {
	script := fmt.Sprintf(`display notification %s with title %s`, appleString(body), appleString(title))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func ntfy(s config.Notify, title, body string) error {
	server := s.Ntfy.Server
	if server == "" {
		server = "https://ntfy.sh"
	}
	req, err := http.NewRequest("POST", strings.TrimRight(server, "/")+"/"+url.PathEscape(s.Ntfy.Topic), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	if s.Ntfy.TokenEnv != "" {
		tok := os.Getenv(s.Ntfy.TokenEnv)
		if tok == "" {
			return fmt.Errorf("%s is not set", s.Ntfy.TokenEnv)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: HTTP %d", resp.StatusCode)
	}
	return nil
}

func telegram(s config.Notify, title, body string) error {
	tok := os.Getenv(s.Telegram.TokenEnv)
	if tok == "" {
		return fmt.Errorf("%s is not set", s.Telegram.TokenEnv)
	}
	payload, _ := json.Marshal(map[string]string{"chat_id": s.Telegram.ChatID, "text": title + "\n" + body})
	resp, err := client.Post("https://api.telegram.org/bot"+tok+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		// A failed request's error carries the URL, and the URL carries the token.
		return fmt.Errorf("telegram: %s", strings.ReplaceAll(err.Error(), tok, "<token>"))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram: HTTP %d", resp.StatusCode)
	}
	return nil
}
