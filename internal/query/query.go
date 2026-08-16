// Package query loads the index and returns ranked, cited chunks for a
// question. It is the retrieval half of the RAG; the language model that
// synthesizes an answer is supplied by the host agent.
package query

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeanT-04/oxford-strat-RAG/internal/index"
)

// Run loads the index at indexPath, searches for q, and writes the top k
// results (with source citations) to w.
func Run(indexPath, q string, k int, w io.Writer) error {
	ix, err := index.Load(indexPath)
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	hits := ix.Search(q, k)
	if len(hits) == 0 {
		fmt.Fprintln(w, "no matching chunks")
		return nil
	}

	for i, h := range hits {
		fmt.Fprintf(w, "[%d] %.4f  %s\n", i+1, h.Score, h.Chunk.Title)
		fmt.Fprintf(w, "    source: %s\n", h.Chunk.Source)
		fmt.Fprintf(w, "    %s\n\n", snippet(h.Chunk.Text, 320))
	}
	return nil
}

// snippet collapses whitespace and truncates s to roughly n chars at a word
// boundary, marking the cut with an ellipsis.
func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
