// Package ingest orchestrates phase 2: read the manifest, extract text from
// each downloaded PDF, chunk it, build a BM25 index, and persist it.
package ingest

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
	"github.com/DeanT-04/oxford-strat-RAG/internal/index"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
	"github.com/DeanT-04/oxford-strat-RAG/internal/text"
)

// Options configures a run.
type Options struct {
	ManifestPath string
	DataDir      string         // directory containing downloaded PDFs
	IndexPath    string         // where to write the index
	Extractor    text.Extractor // nil selects text.Default()
	MaxChunk     int
	MinChunk     int
	Verbose      bool
}

// DocResult records the ingest outcome of one PDF.
type DocResult struct {
	Source string
	Title  string
	Status string // "indexed", "no_text", "error"
	Chunks int
	Error  string
}

// Summary aggregates the run.
type Summary struct {
	Docs      int
	Indexed   int
	NoText    int
	Failed    int
	Chunks    int
	IndexPath string
	Results   []DocResult
}

// Run extracts, chunks, indexes, and saves. Logging goes to stderr; results
// are returned in the Summary for the caller to render.
func Run(opts Options, stderr io.Writer) (Summary, error) {
	var sum Summary

	extractor := opts.Extractor
	if extractor == nil {
		extractor = text.Default()
	}

	m, err := manifest.ReadFile(opts.ManifestPath)
	if err != nil {
		return sum, fmt.Errorf("read manifest: %w", err)
	}

	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = filepath.Dir(opts.ManifestPath)
	}

	lvl := slog.LevelWarn
	if opts.Verbose {
		lvl = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: lvl}))

	var chunks []chunk.Chunk
	for _, e := range m.Entries {
		if e.Status != manifest.StatusDownloaded {
			continue
		}
		sum.Docs++

		pdfPath := filepath.Join(dataDir, e.LocalPath)
		title := e.Title
		if title == "" {
			title = e.LocalPath
		}

		raw, err := extractor.Extract(pdfPath)
		if err != nil {
			sum.Failed++
			sum.Results = append(sum.Results, DocResult{
				Source: e.LocalPath, Title: title, Status: "error", Error: err.Error(),
			})
			logger.Warn("extract failed", "source", e.LocalPath, "err", err)
			continue
		}
		if len(strings.TrimSpace(raw)) == 0 {
			sum.NoText++
			sum.Results = append(sum.Results, DocResult{
				Source: e.LocalPath, Title: title, Status: "no_text",
			})
			logger.Warn("no extractable text (likely scanned)", "source", e.LocalPath)
			continue
		}

		cs := chunk.Split(
			chunk.Doc{ID: e.LocalPath, Source: e.LocalPath, Title: title},
			raw, opts.MaxChunk, opts.MinChunk,
		)
		chunks = append(chunks, cs...)
		sum.Indexed++
		sum.Chunks += len(cs)
		sum.Results = append(sum.Results, DocResult{
			Source: e.LocalPath, Title: title, Status: "indexed", Chunks: len(cs),
		})
	}

	ix := index.Build(chunks)
	if err := ix.Save(opts.IndexPath); err != nil {
		return sum, err
	}
	sum.IndexPath = opts.IndexPath
	return sum, nil
}
