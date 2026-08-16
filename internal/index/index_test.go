package index

import (
	"path/filepath"
	"testing"

	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
)

func sampleChunks() []chunk.Chunk {
	return []chunk.Chunk{
		{ID: "a:0", DocID: "a", Source: "a.pdf", Title: "A", Text: "momentum works in stocks"},
		{ID: "b:0", DocID: "b", Source: "b.pdf", Title: "B", Text: "value investing is classic"},
		{ID: "c:0", DocID: "c", Source: "c.pdf", Title: "C", Text: "momentum and value combined"},
	}
}

func TestBuildStats(t *testing.T) {
	ix := Build(sampleChunks())
	if ix.NumChunks != 3 || ix.NumDocs != 3 {
		t.Fatalf("num chunks/docs = %d/%d, want 3/3", ix.NumChunks, ix.NumDocs)
	}
	if len(ix.DocLen) != 3 || len(ix.Postings) == 0 {
		t.Fatalf("doc lengths / postings not built")
	}
}

func TestSearch(t *testing.T) {
	ix := Build(sampleChunks())

	hits := ix.Search("momentum", 10)
	if len(hits) != 2 {
		t.Fatalf("momentum hits = %d, want 2", len(hits))
	}
	docs := map[string]bool{}
	for _, h := range hits {
		docs[h.Chunk.DocID] = true
	}
	if !docs["a"] || !docs["c"] || docs["b"] {
		t.Fatalf("momentum matched wrong docs: %v", docs)
	}

	// "combined" is unique to c and should rank it first.
	hits = ix.Search("combined", 10)
	if len(hits) != 1 || hits[0].Chunk.DocID != "c" {
		t.Fatalf("combined hits = %+v", hits)
	}
}

func TestSearchNoMatch(t *testing.T) {
	ix := Build(sampleChunks())
	if hits := ix.Search("zzzqwerty", 10); len(hits) != 0 {
		t.Fatalf("expected no hits, got %+v", hits)
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	ix := Build(nil)
	if hits := ix.Search("anything", 10); len(hits) != 0 {
		t.Fatalf("expected no hits on empty index, got %+v", hits)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	ix := Build(sampleChunks())
	p := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.NumChunks != ix.NumChunks || got.NumDocs != ix.NumDocs {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Search("value", 10)) == 0 {
		t.Fatal("loaded index should still search")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error")
	}
}
