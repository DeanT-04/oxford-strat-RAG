package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
	"github.com/DeanT-04/oxford-strat-RAG/internal/index"
)

func TestRunDispatch(t *testing.T) {
	t.Run("no args", func(t *testing.T) {
		var out strings.Builder
		if code := run(nil, &out, &strings.Builder{}); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(out.String(), "vellum") {
			t.Fatalf("usage not printed: %s", out.String())
		}
	})
	t.Run("version", func(t *testing.T) {
		var out strings.Builder
		if code := run([]string{"version"}, &out, &strings.Builder{}); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(out.String(), "vellum") {
			t.Fatalf("version output: %s", out.String())
		}
	})
	t.Run("help", func(t *testing.T) {
		var out strings.Builder
		if code := run([]string{"help"}, &out, &strings.Builder{}); code != 0 {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		var errb strings.Builder
		if code := run([]string{"frobnicate"}, &strings.Builder{}, &errb); code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
	})
}

func TestRunScrapeEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".pdf") {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.4\nok\n%%EOF"))
			return
		}
		fmt.Fprint(w, `<a href="/docs/a.pdf">a</a>`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	// Neutralise ambient env so the test is hermetic.
	t.Setenv("VELLUM_SEED_URL", "")
	t.Setenv("VELLUM_OUTPUT_DIR", "")
	t.Setenv("VELLUM_MANIFEST_PATH", "")

	var out, errb strings.Builder
	code := run([]string{
		"scrape",
		"-url", srv.URL + "/",
		"-out", dir,
		"-manifest", filepath.Join(dir, "m.json"),
		"-politeness", "0",
		"-verbose",
		"-retries", "0",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "a.pdf")); err != nil {
		t.Fatalf("a.pdf missing: %v", err)
	}
}

func TestRunScrapeBadFlag(t *testing.T) {
	var errb strings.Builder
	if code := run([]string{"scrape", "-url", "ftp://nope"}, &strings.Builder{}, &errb); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunScrapeBadEnv(t *testing.T) {
	t.Setenv("VELLUM_CONCURRENCY", "notanint")
	var errb strings.Builder
	if code := run([]string{"scrape"}, &strings.Builder{}, &errb); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunScrapeUnexpectedArg(t *testing.T) {
	t.Setenv("VELLUM_SEED_URL", "")
	var errb strings.Builder
	if code := run([]string{"scrape", "extra"}, &strings.Builder{}, &errb); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunScrapeParseError(t *testing.T) {
	var errb strings.Builder
	if code := run([]string{"scrape", "-concurrency", "abc"}, &strings.Builder{}, &errb); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunQueryEndToEnd(t *testing.T) {
	ix := index.Build([]chunk.Chunk{
		{ID: "a:0", DocID: "a", Source: "a.pdf", Title: "Momentum Paper", Text: "momentum works"},
	})
	p := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(p); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code := run([]string{"query", "-index", p, "-k", "1", "momentum"}, &out, &strings.Builder{})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "Momentum Paper") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestRunQueryUsageError(t *testing.T) {
	var errb strings.Builder
	if code := run([]string{"query"}, &strings.Builder{}, &errb); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunIngestMissingManifest(t *testing.T) {
	var errb strings.Builder
	code := run([]string{"ingest", "-manifest", filepath.Join(t.TempDir(), "nope.json")}, &strings.Builder{}, &errb)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}
