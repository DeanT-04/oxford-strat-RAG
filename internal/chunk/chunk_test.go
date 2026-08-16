package chunk

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitParagraphs(t *testing.T) {
	doc := Doc{ID: "d1", Source: "d1.pdf", Title: "T"}
	raw := "First paragraph here.\n\nSecond paragraph here."
	chunks := Split(doc, raw, 1000, 1)
	if len(chunks) != 2 {
		t.Fatalf("len = %d, want 2", len(chunks))
	}
	if chunks[0].Text != "First paragraph here." || chunks[1].Text != "Second paragraph here." {
		t.Fatalf("chunks = %+v", chunks)
	}
	if chunks[0].DocID != "d1" || chunks[0].Source != "d1.pdf" || chunks[0].Title != "T" {
		t.Fatalf("metadata = %+v", chunks[0])
	}
	if chunks[0].ID != "d1:0" || chunks[1].ID != "d1:1" {
		t.Fatalf("ids = %q, %q", chunks[0].ID, chunks[1].ID)
	}
}

func TestSplitFiltersShort(t *testing.T) {
	doc := Doc{ID: "d1", Source: "d1.pdf", Title: "T"}
	raw := "tiny\n\nA real paragraph with enough characters to survive the minimum length filter."
	chunks := Split(doc, raw, 1000, 20)
	if len(chunks) != 1 {
		t.Fatalf("len = %d, want 1 (short chunk dropped)", len(chunks))
	}
}

func TestSplitLongBlock(t *testing.T) {
	doc := Doc{ID: "d1", Source: "d1.pdf", Title: "T"}
	// Two sentences separated by ". " exceeding maxLen so it must split.
	raw := strings.Repeat("a", 50) + ". " + strings.Repeat("b", 50) + "."
	chunks := Split(doc, raw, 60, 1)
	if len(chunks) != 2 {
		t.Fatalf("len = %d, want 2", len(chunks))
	}
	if !strings.HasPrefix(chunks[0].Text, "aaaa") || !strings.HasPrefix(chunks[1].Text, "bbbb") {
		t.Fatalf("unexpected split: %q | %q", chunks[0].Text, chunks[1].Text)
	}
}

func TestSplitBlocks(t *testing.T) {
	got := splitBlocks("a\r\n\r\nb\fc")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitBlocks = %q, want %q", got, want)
	}
}

func TestCollapseWS(t *testing.T) {
	if got := collapseWS("  a \t b\n c  "); got != "a b c" {
		t.Fatalf("collapseWS = %q", got)
	}
}

func TestSplitLong(t *testing.T) {
	if got := splitLong("short", 100); !reflect.DeepEqual(got, []string{"short"}) {
		t.Fatalf("short block: %v", got)
	}
	sents := []string{"one.", "two.", "three.", "four."}
	block := strings.Join(sents, " ")
	out := splitLong(block, 12)
	if len(out) < 2 {
		t.Fatalf("expected multiple chunks, got %v", out)
	}
}
