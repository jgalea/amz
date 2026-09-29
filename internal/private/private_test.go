package private

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTightensExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acct")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(dir, "cookies.json")
	if err := os.WriteFile(jar, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Dir(dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(jar, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(jar); st.Mode().Perm() != 0o600 {
		t.Errorf("jar mode %v", st.Mode().Perm())
	}
	f, err := Create(filepath.Join(dir, "orders.csv"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st, _ := os.Stat(filepath.Join(dir, "orders.csv")); st.Mode().Perm() != 0o600 {
		t.Errorf("csv mode %v", st.Mode().Perm())
	}
}
