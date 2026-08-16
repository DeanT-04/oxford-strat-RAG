package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAndRoundTrip(t *testing.T) {
	entries := []Entry{
		{URL: "https://x/a.pdf", Status: StatusDownloaded, Size: 10, SHA256: "abc"},
		{URL: "https://x/b.pdf", Status: StatusFailed, Error: "boom"},
	}
	m := New("https://x/", entries)
	if m.Count != 2 {
		t.Fatalf("count = %d", m.Count)
	}
	if m.Seed != "https://x/" {
		t.Fatalf("seed = %s", m.Seed)
	}
	if m.GeneratedAt.IsZero() {
		t.Fatal("generated_at must not be zero")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "manifest.json")
	if err := m.WriteFile(p); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 || got.Seed != "https://x/" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d", len(got.Entries))
	}
	if got.Entries[0].URL != "https://x/a.pdf" || got.Entries[0].SHA256 != "abc" {
		t.Fatalf("entry[0] = %+v", got.Entries[0])
	}
}

func TestReadFileMissing(t *testing.T) {
	if _, err := ReadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadFileInvalid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestEncodeIndented(t *testing.T) {
	var sb strings.Builder
	if err := New("https://x/", nil).Encode(&sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "\"generated_at\"") {
		t.Fatalf("unexpected output: %s", sb.String())
	}
}

func TestWriteFileMkdirError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New("https://x/", nil).WriteFile(filepath.Join(blocker, "m.json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestKindOfDefault(t *testing.T) {
	if (Entry{}).KindOf() != KindPDF {
		t.Fatal("empty kind must default to pdf")
	}
	if (Entry{Kind: KindHTML}).KindOf() != KindHTML {
		t.Fatal("explicit kind must be preserved")
	}
}

func TestReferenceRoundTrip(t *testing.T) {
	m := New("https://x/", []Entry{
		{URL: "https://papers.ssrn.com/abc", Kind: KindPDF, Status: StatusReference, Host: "external"},
	})
	dir := t.TempDir()
	p := filepath.Join(dir, "m.json")
	if err := m.WriteFile(p); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	e := got.Entries[0]
	if e.Status != StatusReference || e.Host != "external" || e.Kind != KindPDF {
		t.Fatalf("entry = %+v", e)
	}
}
