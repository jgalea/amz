package config

import (
	"encoding/json"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"os"
	"path/filepath"
)

// Settings is ~/.amz/config.json: everything that is not an account.
type Settings struct {
	Notify   Notify   `json:"notify"`
	Mail     Mail     `json:"mail"`
	Invoices Invoices `json:"invoices"`
}

type Notify struct {
	// MacOS shows a notification centre banner (osascript).
	MacOS bool `json:"macos"`
	Ntfy  struct {
		Server string `json:"server,omitempty"`
		Topic  string `json:"topic,omitempty"`
		// TokenEnv names the environment variable holding a bearer token,
		// so the token itself never sits in the file.
		TokenEnv string `json:"token_env,omitempty"`
	} `json:"ntfy"`
	Telegram struct {
		TokenEnv string `json:"token_env,omitempty"`
		ChatID   string `json:"chat_id,omitempty"`
	} `json:"telegram"`
}

// Mail is the mailbox `amz notices` scans over IMAP. Gmail works
// with an app password and imap.gmail.com:993.
type Mail struct {
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	User        string `json:"user,omitempty"`
	PasswordEnv string `json:"password_env,omitempty"`
	Folder      string `json:"folder,omitempty"`
}

type Invoices struct {
	// Dir is the accountant folder root; the layout underneath is
	// <year>/Q<n>/Amazon/<account>/.
	Dir string `json:"dir,omitempty"`
}

func settingsPath() string { return filepath.Join(Dir(), "config.json") }

func LoadSettings() (Settings, error) {
	var s Settings
	s.Notify.MacOS = true
	raw, err := os.ReadFile(settingsPath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("%s: %w", settingsPath(), err)
	}
	return s, nil
}

func (s Settings) Save() error {
	if err := private.Dir(Dir()); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), append(raw, '\n'), 0o600)
}
