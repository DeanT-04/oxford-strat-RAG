// Package app wires config, fetching, crawling, downloading, and manifest
// writing into one runnable flow. It is the composition root; cmd/vellum is
// only a thin flag parser around it.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
	"github.com/DeanT-04/oxford-strat-RAG/internal/crawl"
	"github.com/DeanT-04/oxford-strat-RAG/internal/download"
	"github.com/DeanT-04/oxford-strat-RAG/internal/fetch"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
)

// Summary reports the outcome of a run.
type Summary struct {
	Discovered   int
	Downloaded   int
	Skipped      int
	Failed       int
	ManifestPath string
}

// Run executes a full scrape: validate config, discover PDFs, download
// them, and write the manifest. All output goes through the provided
// writers so it is fully testable.
func Run(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) (Summary, error) {
	var sum Summary
	if err := cfg.Validate(); err != nil {
		return sum, err
	}

	logger := newLogger(stderr, cfg.Verbose)
	client := fetch.New(fetch.Options{
		UserAgent:  cfg.UserAgent,
		Timeout:    cfg.Timeout,
		Retries:    cfg.Retries,
		Backoff:    500 * time.Millisecond,
		Politeness: cfg.Politeness,
	})

	cr, err := crawl.New(client, cfg.SeedURL, cfg.MaxDepth)
	if err != nil {
		return sum, err
	}

	logger.Info("discovering PDFs", "seed", cfg.SeedURL)
	links, err := cr.Discover(ctx)
	if err != nil {
		return sum, err
	}
	sum.Discovered = len(links)

	if cfg.DryRun {
		for _, l := range links {
			fmt.Fprintf(stdout, "%s\t%s\n", l.URL, l.Title)
		}
		fmt.Fprintf(stdout, "discovered %d PDF link(s)\n", len(links))
		return sum, nil
	}

	if len(links) == 0 {
		fmt.Fprintln(stdout, "no PDF links discovered")
		return sum, nil
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return sum, fmt.Errorf("create output dir: %w", err)
	}

	urls := make([]string, len(links))
	for i, l := range links {
		urls[i] = l.URL
	}

	pool := download.New(client, cfg.OutputDir, cfg.MaxFileSize, cfg.Resume)
	results := pool.DownloadAll(ctx, urls, cfg.ResolveConcurrency())

	if err := ctx.Err(); err != nil {
		return sum, err
	}

	entries := make([]manifest.Entry, 0, len(links))
	for i, r := range results {
		e := manifest.Entry{
			URL:         r.URL,
			FinalURL:    r.FinalURL,
			LocalPath:   r.LocalPath,
			Size:        r.Size,
			SHA256:      r.SHA256,
			ContentType: r.ContentType,
			Status:      r.Status,
			Error:       r.Err,
			FetchedAt:   r.FetchedAt,
		}
		if i < len(links) {
			e.FoundOn = links[i].FoundOn
			e.Title = links[i].Title
		}
		switch r.Status {
		case manifest.StatusDownloaded:
			sum.Downloaded++
		case manifest.StatusSkipped:
			sum.Skipped++
		default:
			sum.Failed++
		}
		entries = append(entries, e)
	}

	m := manifest.New(cfg.SeedURL, entries)
	if err := m.WriteFile(cfg.ManifestPath); err != nil {
		return sum, err
	}
	sum.ManifestPath = cfg.ManifestPath

	logger.Info("run complete",
		"discovered", sum.Discovered,
		"downloaded", sum.Downloaded,
		"skipped", sum.Skipped,
		"failed", sum.Failed,
	)
	fmt.Fprintf(stdout, "discovered: %d, downloaded: %d, skipped: %d, failed: %d\n",
		sum.Discovered, sum.Downloaded, sum.Skipped, sum.Failed)
	fmt.Fprintf(stdout, "manifest: %s\n", cfg.ManifestPath)
	return sum, nil
}

// newLogger returns a structured logger writing to w. Verbose mode logs at
// Info level; otherwise only warnings/errors are emitted.
func newLogger(w io.Writer, verbose bool) *slog.Logger {
	lvl := slog.LevelWarn
	if verbose {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl}))
}
