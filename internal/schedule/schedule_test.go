package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesOnePlistPerJob(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	jobs := Jobs("work")
	paths, err := Install(dir, "com.example.", "/usr/local/bin/amz", logs, jobs, map[string]string{"AMZ_CONFIG_DIR": "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("wrote %d plists, want 4", len(paths))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "com.example.amz-sync.plist"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"<string>com.example.amz-sync</string>", "<string>/usr/local/bin/amz</string>", "<string>sync</string>", "<string>--account</string>", "<string>work</string>", "<key>StartInterval</key><integer>86400</integer>", "<key>AMZ_CONFIG_DIR</key><string>/tmp/x</string>", filepath.Join(logs, "sync.log")} {
		if !strings.Contains(s, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if _, err := os.Stat(logs); err != nil {
		t.Error("log dir not created")
	}
	watch, _ := os.ReadFile(filepath.Join(dir, "com.example.amz-watch.plist"))
	if strings.Contains(string(watch), "--account") {
		t.Error("watch run takes no account")
	}
	if !strings.Contains(string(watch), "<integer>21600</integer>") {
		t.Error("watch interval should be six hours")
	}
}

func TestPlistEscapes(t *testing.T) {
	p := Plist("l", "/bin/c", Job{Name: "x", Args: []string{`a<b>&"c"`}, Interval: 60}, "/l", nil)
	if !strings.Contains(p, "a&lt;b&gt;&amp;&quot;c&quot;") {
		t.Error("arguments must be XML-escaped")
	}
	if strings.Contains(p, "EnvironmentVariables") {
		t.Error("no env block without env")
	}
}

func TestAnnounce(t *testing.T) {
	dir := t.TempDir()
	if err := Announce(dir, "amz", "hello"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "notifications.jsonl"))
	if !strings.Contains(string(raw), `"job":"amz"`) || !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("feed line: %q", raw)
	}
}
