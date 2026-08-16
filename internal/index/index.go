// Package index implements a lightweight BM25 retrieval index with JSON
// persistence. It is pure Go and in-memory, so querying a corpus of a few
// dozen papers costs microseconds — no GPU, no external service.
package index

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
	"github.com/DeanT-04/oxford-strat-RAG/internal/text"
)

// BM25 tuning parameters.
const (
	k1 = 1.5
	b  = 0.75
)

// Version of the on-disk format.
const Version = 1

// Posting records a term occurrence in one chunk (doc).
type Posting struct {
	Doc  int `json:"d"` // chunk index into Index.Chunks
	Freq int `json:"f"` // term frequency within that chunk
}

// Index is the serializable BM25 index over all chunks.
type Index struct {
	Version   int                  `json:"version"`
	BuiltAt   time.Time            `json:"built_at"`
	NumDocs   int                  `json:"num_docs"`
	NumChunks int                  `json:"num_chunks"`
	Chunks    []chunk.Chunk        `json:"chunks"`
	AvgLen    float64              `json:"avg_len"`
	DocLen    []int                `json:"doc_len"`
	Postings  map[string][]Posting `json:"postings"`
}

// Hit is a ranked retrieval result.
type Hit struct {
	Chunk chunk.Chunk
	Score float64
}

// Build constructs an Index from chunks by tokenizing each chunk and building
// the inverted index.
func Build(chunks []chunk.Chunk) *Index {
	ix := &Index{
		Version:   Version,
		BuiltAt:   time.Now(),
		NumChunks: len(chunks),
		Chunks:    chunks,
		DocLen:    make([]int, len(chunks)),
		Postings:  make(map[string][]Posting),
	}

	total := 0
	for i, c := range chunks {
		tokens := text.Tokenize(c.Text)
		ix.DocLen[i] = len(tokens)
		total += len(tokens)

		counts := make(map[string]int)
		for _, t := range tokens {
			counts[t]++
		}
		for term, freq := range counts {
			ix.Postings[term] = append(ix.Postings[term], Posting{Doc: i, Freq: freq})
		}
	}
	if len(chunks) > 0 {
		ix.AvgLen = float64(total) / float64(len(chunks))
	}

	docs := make(map[string]bool)
	for _, c := range chunks {
		docs[c.DocID] = true
	}
	ix.NumDocs = len(docs)
	return ix
}

// Search ranks chunks against query using BM25 and returns the top k, highest
// score first. It returns fewer than k results when fewer chunks match.
func (ix *Index) Search(query string, k int) []Hit {
	if k <= 0 {
		k = 10
	}
	avg := ix.AvgLen
	if avg <= 0 {
		avg = 1
	}

	// Deduplicate query terms to avoid double-counting repeated words.
	seen := make(map[string]bool)
	var qterms []string
	for _, t := range text.Tokenize(query) {
		if !seen[t] {
			seen[t] = true
			qterms = append(qterms, t)
		}
	}

	scores := make([]float64, len(ix.Chunks))
	n := float64(len(ix.Chunks))
	for _, term := range qterms {
		postings := ix.Postings[term]
		if len(postings) == 0 {
			continue
		}
		idf := math.Log((n-float64(len(postings))+0.5)/(float64(len(postings))+0.5) + 1)
		for _, p := range postings {
			dl := float64(ix.DocLen[p.Doc])
			if dl <= 0 {
				dl = 1
			}
			denom := float64(p.Freq) + k1*(1-b+b*dl/avg)
			scores[p.Doc] += idf * (float64(p.Freq) * (k1 + 1)) / denom
		}
	}

	type scored struct {
		idx int
		s   float64
	}
	cand := make([]scored, 0, len(scores))
	for i, s := range scores {
		if s > 0 {
			cand = append(cand, scored{i, s})
		}
	}
	sort.Slice(cand, func(a, b int) bool {
		if cand[a].s != cand[b].s {
			return cand[a].s > cand[b].s
		}
		return cand[a].idx < cand[b].idx
	})
	if len(cand) > k {
		cand = cand[:k]
	}

	hits := make([]Hit, len(cand))
	for i, c := range cand {
		hits[i] = Hit{Chunk: ix.Chunks[c.idx], Score: c.s}
	}
	return hits
}

// Save writes the index atomically to path as indented JSON.
func (ix *Index) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("index: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ix); err != nil {
		tmp.Close()
		return fmt.Errorf("index: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("index: rename: %w", err)
	}
	return nil
}

// Load reads an index previously written by Save.
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ix Index
	if err := json.NewDecoder(f).Decode(&ix); err != nil {
		return nil, fmt.Errorf("index: decode: %w", err)
	}
	return &ix, nil
}
