// Package schedule writes launchd agents that run amz on a
// cadence, and tells a local jobs registry about them when one exists.
package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Job is one recurring amz invocation.
type Job struct {
	Name     string
	Args     []string
	Interval time.Duration
}

// Jobs is the default set: a sync in the small hours, price watches
// every few hours, the return-window and health checks once a day.
var labelRe = regexp.MustCompile(`^[A-Za-z0-9.-]*$`)

func Jobs(account string) []Job {
	acct := []string{}
	if account != "" {
		acct = []string{"--account", account}
	}
	return []Job{
		{"sync", append([]string{"sync", "--market", "all"}, acct...), 24 * time.Hour},
		{"watch", []string{"watch", "run"}, 6 * time.Hour},
		{"returns", append([]string{"returns", "window", "--alert"}, acct...), 24 * time.Hour},
		{"health", append([]string{"health", "--alert"}, acct...), 24 * time.Hour},
	}
}

// Plist renders the launchd property list for a job.
func Plist(label, binary string, j Job, logDir string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + xml(label) + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	fmt.Fprintf(&b, "    <string>%s</string>\n", xml(binary))
	for _, a := range j.Args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xml(a))
	}
	b.WriteString("  </array>\n")
	fmt.Fprintf(&b, "  <key>StartInterval</key><integer>%d</integer>\n", int(j.Interval.Seconds()))
	fmt.Fprintf(&b, "  <key>StandardOutPath</key><string>%s</string>\n", xml(filepath.Join(logDir, j.Name+".log")))
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key><string>%s</string>\n", xml(filepath.Join(logDir, j.Name+".log")))
	if len(env) > 0 {
		b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		for k, v := range env {
			fmt.Fprintf(&b, "    <key>%s</key><string>%s</string>\n", xml(k), xml(v))
		}
		b.WriteString("  </dict>\n")
	}
	b.WriteString("  <key>RunAtLoad</key><false/>\n  <key>Nice</key><integer>5</integer>\n</dict>\n</plist>\n")
	return b.String()
}

func xml(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Install writes one plist per job into dir (normally
// ~/Library/LaunchAgents) and returns the paths. Nothing is loaded; the
// caller prints the launchctl lines or runs them.
func Install(dir, labelPrefix, binary, logDir string, jobs []Job, env map[string]string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	var paths []string
	if !labelRe.MatchString(labelPrefix) {
		return nil, fmt.Errorf("label prefix %q may only hold letters, digits, dots and dashes", labelPrefix)
	}
	for _, j := range jobs {
		label := labelPrefix + "amz-" + j.Name
		p := filepath.Join(dir, label+".plist")
		if err := os.WriteFile(p, []byte(Plist(label, binary, j, logDir, env)), 0o644); err != nil {
			return paths, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// Registry is the local jobs registry (the `jobz` tool) when present:
// a directory with a notifications.jsonl feed. Launchd agents are
// picked up from ~/Library/LaunchAgents by label prefix on its own;
// this only announces the install on the feed.
func Registry() string {
	if d := os.Getenv("JOBS_DIR"); d != "" {
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(home, ".claude", "jobs")
	if _, err := os.Stat(d); err == nil {
		return d
	}
	return ""
}

// Announce appends a line to the registry feed.
func Announce(dir, job, message string) error {
	f, err := os.OpenFile(filepath.Join(dir, "notifications.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, _ := json.Marshal(map[string]string{"job": job, "message": message, "ts": time.Now().Format(time.RFC3339)})
	_, err = f.Write(append(raw, '\n'))
	return err
}
