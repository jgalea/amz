// Package browser drives a real Chrome instance against a dedicated,
// persistent profile so a hand-done login survives between runs.
package browser

import (
	"context"
	"fmt"
	"github.com/jgalea/amz/internal/private"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

type Session struct {
	Ctx    context.Context
	cancel []context.CancelFunc
}

type Options struct {
	ProfileDir string
	Headless   bool
	Timeout    time.Duration
}

func chromePath() string {
	if p := os.Getenv("AMZ_CHROME"); p != "" {
		return p
	}
	candidates := map[string][]string{
		"darwin": {
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		},
		"linux": {
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		},
		"windows": {
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		},
	}
	// Per-user installs live under LOCALAPPDATA. Only trust it as an absolute
	// path, or an unset variable would resolve chrome.exe against the cwd.
	if local := os.Getenv("LOCALAPPDATA"); filepath.IsAbs(local) {
		candidates["windows"] = append(candidates["windows"],
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`))
	}
	for _, p := range candidates[runtime.GOOS] {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func Open(parent context.Context, o Options) (*Session, error) {
	exec := chromePath()
	if exec == "" {
		return nil, fmt.Errorf("no Chrome found; set AMZ_CHROME to the browser binary")
	}
	if err := private.Dir(o.ProfileDir); err != nil {
		return nil, err
	}
	if inUse(o.ProfileDir) {
		return nil, fmt.Errorf("another Chrome is already using %s; close that window and try again", o.ProfileDir)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exec),
		chromedp.UserDataDir(o.ProfileDir),
		chromedp.Flag("headless", o.Headless),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.WindowSize(1440, 960),
	)

	s := &Session{}
	ctx, cancel := chromedp.NewExecAllocator(parent, opts...)
	s.cancel = append(s.cancel, cancel)
	ctx, cancel = chromedp.NewContext(ctx)
	s.cancel = append(s.cancel, cancel)
	if o.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		s.cancel = append(s.cancel, cancel)
	}
	s.Ctx = ctx
	return s, nil
}

// inUse reports whether a live Chrome still holds the profile. Chrome
// answers a second launch on a locked profile by handing the URL to the
// running instance and exiting, which reads as a start failure here.
func inUse(dir string) bool {
	if runtime.GOOS == "windows" {
		// Windows Chrome holds a delete-on-close "lockfile" instead of a
		// SingletonLock symlink, so it only exists while Chrome is running.
		_, err := os.Stat(filepath.Join(dir, "lockfile"))
		return err == nil
	}
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	parts := strings.Split(target, "-")
	if len(parts) < 2 {
		return false
	}
	pid, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func (s *Session) Close() {
	for i := len(s.cancel) - 1; i >= 0; i-- {
		s.cancel[i]()
	}
}
