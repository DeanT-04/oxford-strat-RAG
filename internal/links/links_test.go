package links

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripAndList(t *testing.T) {
	d := New("https://example.com/resources/links/", map[string][]Item{
		"data":              {{Name: "CSI", URL: "http://www.csidata.com/"}},
		"digital-libraries": {{Name: "JSTOR", URL: "https://www.jstor.org/"}},
	})
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "links.json")
	if err := d.WriteFile(p); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "links" || len(got.Groups) != 2 {
		t.Fatalf("doc = %+v", got)
	}

	var sb strings.Builder
	got.List("", &sb)
	if !strings.Contains(sb.String(), "digital-libraries:") || !strings.Contains(sb.String(), "JSTOR") {
		t.Fatalf("list output = %q", sb.String())
	}
	// Deterministic ordering: "data" sorts before "digital-libraries".
	if strings.Index(sb.String(), "data:") > strings.Index(sb.String(), "digital-libraries:") {
		t.Fatalf("groups not sorted: %q", sb.String())
	}

	var filtered strings.Builder
	got.List("data", &filtered)
	if strings.Contains(filtered.String(), "digital-libraries") {
		t.Fatalf("group filter ignored: %q", filtered.String())
	}
}

func TestReadFileMissing(t *testing.T) {
	if _, err := ReadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteFileMkdirError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New("x", nil).WriteFile(filepath.Join(blocker, "l.json")); err == nil {
		t.Fatal("expected error")
	}
}
