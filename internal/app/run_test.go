package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
)

// siteServer serves a small two-level site with real and fake PDFs.
func siteServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/resources/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/resources/articles/">articles</a> <a href="/resources/books/">books</a>`)
	})
	mux.HandleFunc("/resources/articles/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/uploads/turtle.pdf">Turtle Rules</a> <a href="/uploads/fake.pdf">Fake</a>`)
	})
	mux.HandleFunc("/resources/books/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/uploads/book.pdf">Book</a>`)
	})
	mux.HandleFunc("/uploads/turtle.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4\nturtle\n%%EOF"))
	})
	mux.HandleFunc("/uploads/book.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4\nbook\n%%EOF"))
	})
	mux.HandleFunc("/uploads/fake.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>not a pdf</html>")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func baseConfig(srv *httptest.Server, dir string) config.Config {
	cfg := config.Default()
	cfg.SeedURL = srv.URL + "/resources/"
	cfg.OutputDir = dir
	cfg.ManifestPath = filepath.Join(dir, "manifest.json")
	cfg.Politeness = 0
	cfg.Retries = 0
	cfg.Concurrency = 2
	return cfg
}

func TestRunEndToEnd(t *testing.T) {
	srv := siteServer(t)
	dir := t.TempDir()
	cfg := baseConfig(srv, dir)

	var out, errb strings.Builder
	sum, err := Run(context.Background(), cfg, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, errb.String())
	}
	if sum.Discovered != 3 {
		t.Fatalf("discovered = %d, want 3", sum.Discovered)
	}
	if sum.Downloaded != 2 {
		t.Fatalf("downloaded = %d, want 2", sum.Downloaded)
	}
	if sum.Failed != 1 {
		t.Fatalf("failed = %d, want 1", sum.Failed)
	}

	m, err := manifest.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Count != 3 {
		t.Fatalf("manifest count = %d, want 3", m.Count)
	}
	if _, err := os.Stat(filepath.Join(dir, "turtle.pdf")); err != nil {
		t.Fatalf("turtle.pdf missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "book.pdf")); err != nil {
		t.Fatalf("book.pdf missing: %v", err)
	}
}

func TestRunDryRun(t *testing.T) {
	srv := siteServer(t)
	dir := t.TempDir()
	cfg := baseConfig(srv, dir)
	cfg.DryRun = true

	var out, errb strings.Builder
	sum, err := Run(context.Background(), cfg, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Discovered != 3 {
		t.Fatalf("discovered = %d, want 3", sum.Discovered)
	}
	if !strings.Contains(out.String(), "turtle.pdf") {
		t.Fatalf("dry run should list URLs, got: %s", out.String())
	}
	if _, err := os.Stat(cfg.ManifestPath); !os.IsNotExist(err) {
		t.Fatal("dry run must not write a manifest")
	}
}

func TestRunInvalidConfig(t *testing.T) {
	cfg := config.Default()
	cfg.SeedURL = "ftp://nope"
	if _, err := Run(context.Background(), cfg, &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunNoLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>nothing here</body></html>")
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.SeedURL = srv.URL + "/"
	cfg.OutputDir = t.TempDir()
	cfg.ManifestPath = filepath.Join(t.TempDir(), "m.json")
	cfg.Politeness = 0
	cfg.Retries = 0

	var out strings.Builder
	if _, err := Run(context.Background(), cfg, &out, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no links discovered") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestNewLogger(t *testing.T) {
	var w strings.Builder
	if newLogger(&w, false) == nil {
		t.Fatal("nil logger (quiet)")
	}
	if newLogger(&w, true) == nil {
		t.Fatal("nil logger (verbose)")
	}
}

func TestRunOutputDirError(t *testing.T) {
	srv := siteServer(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig(srv, filepath.Join(blocker, "sub"))
	if _, err := Run(context.Background(), cfg, &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunManifestError(t *testing.T) {
	srv := siteServer(t)
	cfg := baseConfig(srv, t.TempDir())
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(blocker, "manifest.json")
	if _, err := Run(context.Background(), cfg, &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("expected error")
	}
}

// articleSite serves a resources index with a review heading plus the article
// page it links to, so HTML discovery and rating enrichment can be tested.
func articleSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/resources/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<h2>Reviews of Trading Strategies</h2><a href="/trading-strategies/nr7/">NR7 Pattern</a>`)
	})
	mux.HandleFunc("/trading-strategies/nr7/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		fmt.Fprint(w, `<html><head><title>NR7 Pattern</title></head><body>
			<h1>NR7 Pattern</h1><p>The narrowest range of seven bars.</p>
			<p>Rating: A / <strong>B</strong> / C / D</p>
		</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunHTMLArticle(t *testing.T) {
	srv := articleSite(t)
	dir := t.TempDir()
	cfg := baseConfig(srv, dir)
	cfg.Kinds = "pdf,html"

	var out, errb strings.Builder
	sum, err := Run(context.Background(), cfg, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, errb.String())
	}
	if sum.Discovered != 1 || sum.Downloaded != 1 {
		t.Fatalf("discovered/downloaded = %d/%d", sum.Discovered, sum.Downloaded)
	}

	m, err := manifest.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Count != 1 {
		t.Fatalf("manifest count = %d", m.Count)
	}
	e := m.Entries[0]
	if e.Kind != "html" {
		t.Fatalf("kind = %q", e.Kind)
	}
	if e.Rating != "B" {
		t.Fatalf("rating = %q, want B", e.Rating)
	}
	if e.Title != "NR7 Pattern" {
		t.Fatalf("title = %q", e.Title)
	}
	if _, err := os.Stat(filepath.Join(dir, "nr7.html")); err != nil {
		t.Fatalf("nr7.html missing: %v", err)
	}
}

func TestRunKindsPDFOnly(t *testing.T) {
	srv := articleSite(t)
	dir := t.TempDir()
	cfg := baseConfig(srv, dir)
	cfg.Kinds = "pdf"

	var out, errb strings.Builder
	sum, err := Run(context.Background(), cfg, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Discovered != 0 {
		t.Fatalf("pdf-only run should find no PDFs, got %d", sum.Discovered)
	}
}
