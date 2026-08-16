package query

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
	"github.com/DeanT-04/oxford-strat-RAG/internal/index"
)

func TestRun(t *testing.T) {
	ix := index.Build([]chunk.Chunk{
		{ID: "a:0", DocID: "a", Source: "a.pdf", Title: "Momentum Paper", Text: "momentum works across asset classes"},
	})
	p := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(p); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := Run(p, "momentum", 5, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Momentum Paper") {
		t.Fatalf("output missing title: %s", out.String())
	}
	if !strings.Contains(out.String(), "source: a.pdf") {
		t.Fatalf("output missing source: %s", out.String())
	}
}

func TestRunNoMatch(t *testing.T) {
	ix := index.Build(nil)
	p := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(p); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Run(p, "zzzqwerty", 5, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no matching chunks") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestRunMissingIndex(t *testing.T) {
	if err := Run(filepath.Join(t.TempDir(), "nope.json"), "q", 5, &strings.Builder{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSnippet(t *testing.T) {
	if got := snippet("short text", 100); got != "short text" {
		t.Fatalf("short snippet = %q", got)
	}
	long := strings.Repeat("word ", 50)
	got := snippet(long, 30)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
	if len(got) > 34 { // n bytes + 3-byte ellipsis + slack
		t.Fatalf("snippet too long: %d", len(got))
	}
}
