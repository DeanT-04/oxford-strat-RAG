package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/oxford-strat-RAG/internal/index"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
)

type stubExtractor struct {
	texts map[string]string
	err   error
}

func (s stubExtractor) Extract(path string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.texts[path], nil
}

func writeManifest(t *testing.T, dataDir string) string {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.pdf", "b.pdf", "scan.pdf"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.New("https://x", []manifest.Entry{
		{URL: "https://x/a.pdf", LocalPath: "a.pdf", Title: "Paper A", Status: manifest.StatusDownloaded},
		{URL: "https://x/b.pdf", LocalPath: "b.pdf", Title: "Paper B", Status: manifest.StatusDownloaded},
		{URL: "https://x/scan.pdf", LocalPath: "scan.pdf", Title: "Scan", Status: manifest.StatusDownloaded},
		{URL: "https://x/fail.pdf", LocalPath: "fail.pdf", Title: "Fail", Status: manifest.StatusFailed},
	})
	p := filepath.Join(filepath.Dir(dataDir), "manifest.json")
	if err := m.WriteFile(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	manifestPath := writeManifest(t, dataDir)

	ext := stubExtractor{texts: map[string]string{
		filepath.Join(dataDir, "a.pdf"):    "momentum is a robust anomaly across markets.\n\nIt works in many asset classes.",
		filepath.Join(dataDir, "b.pdf"):    "value investing has a long history.",
		filepath.Join(dataDir, "scan.pdf"): "",
	}}
	indexPath := filepath.Join(dir, "index.json")

	sum, err := Run(Options{
		ManifestPath: manifestPath,
		DataDir:      dataDir,
		IndexPath:    indexPath,
		Extractor:    ext,
		MinChunk:     1,
	}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Docs != 3 {
		t.Fatalf("docs = %d, want 3 (failed entry not counted)", sum.Docs)
	}
	if sum.Indexed != 2 || sum.NoText != 1 || sum.Failed != 0 {
		t.Fatalf("indexed/noText/failed = %d/%d/%d", sum.Indexed, sum.NoText, sum.Failed)
	}
	if sum.Chunks == 0 {
		t.Fatal("expected chunks to be produced")
	}
	if _, err := index.Load(indexPath); err != nil {
		t.Fatalf("index not loadable: %v", err)
	}
}

func TestRunExtractError(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	manifestPath := writeManifest(t, dataDir)

	ext := stubExtractor{err: errors.New("boom")}
	sum, err := Run(Options{
		ManifestPath: manifestPath,
		DataDir:      dataDir,
		IndexPath:    filepath.Join(dir, "index.json"),
		Extractor:    ext,
	}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 3 {
		t.Fatalf("failed = %d, want 3", sum.Failed)
	}
}

func TestRunMissingManifest(t *testing.T) {
	if _, err := Run(Options{
		ManifestPath: filepath.Join(t.TempDir(), "nope.json"),
		DataDir:      t.TempDir(),
		IndexPath:    filepath.Join(t.TempDir(), "index.json"),
	}, &strings.Builder{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunHTMLDispatch(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	html := `<html><head><title>NR7 Pattern</title></head><body>
		<h1>NR7 Pattern</h1><p>The NR7 setup is the narrowest range of the last seven bars.</p>
	</body></html>`
	if err := os.WriteFile(filepath.Join(dataDir, "nr7.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	m := manifest.New("https://x", []manifest.Entry{
		{URL: "https://x/trading-strategies/nr7/", LocalPath: "nr7.html", Title: "NR7", Kind: manifest.KindHTML, Status: manifest.StatusDownloaded},
		{URL: "https://x/links/", LocalPath: "links.json", Kind: manifest.KindLinks, Status: manifest.StatusDownloaded},
	})
	mp := filepath.Join(dir, "manifest.json")
	if err := m.WriteFile(mp); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(dir, "index.json")
	sum, err := Run(Options{ManifestPath: mp, DataDir: dataDir, IndexPath: indexPath, MinChunk: 1}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	// links entry skipped; only the html doc counts.
	if sum.Docs != 1 || sum.Indexed != 1 {
		t.Fatalf("docs/indexed = %d/%d", sum.Docs, sum.Indexed)
	}

	ix, err := index.Load(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	c := ix.Chunks[0]
	if c.Kind != manifest.KindHTML {
		t.Fatalf("chunk kind = %q", c.Kind)
	}
	if c.SourceURL != "https://x/trading-strategies/nr7/" {
		t.Fatalf("chunk source_url = %q", c.SourceURL)
	}
	if !strings.Contains(c.Text, "NR7") {
		t.Fatalf("chunk text missing content: %q", c.Text)
	}
}
