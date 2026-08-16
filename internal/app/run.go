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
	"path/filepath"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
	"github.com/DeanT-04/oxford-strat-RAG/internal/crawl"
	"github.com/DeanT-04/oxford-strat-RAG/internal/download"
	"github.com/DeanT-04/oxford-strat-RAG/internal/fetch"
	"github.com/DeanT-04/oxford-strat-RAG/internal/htmltext"
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

// target is a discovered download candidate with its metadata.
type target struct {
	URL     string
	Kind    string
	Title   string
	FoundOn string
}

// Run executes a full scrape: validate config, discover links of the
// configured kinds, download them, and write the manifest. All output goes
// through the provided writers so it is fully testable.
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

	targets, err := discover(ctx, cfg, client, logger)
	if err != nil {
		return sum, err
	}
	sum.Discovered = len(targets)

	if cfg.DryRun {
		for _, t := range targets {
			fmt.Fprintf(stdout, "%s\t%s\n", t.URL, t.Title)
		}
		fmt.Fprintf(stdout, "discovered %d link(s)\n", len(targets))
		return sum, nil
	}

	if len(targets) == 0 {
		fmt.Fprintln(stdout, "no links discovered")
		return sum, nil
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return sum, fmt.Errorf("create output dir: %w", err)
	}

	dtargets := make([]download.Target, len(targets))
	for i, t := range targets {
		dtargets[i] = download.Target{URL: t.URL, Kind: t.Kind}
	}
	pool := download.New(client, cfg.OutputDir, cfg.MaxFileSize, cfg.Resume)
	results := pool.DownloadTargets(ctx, dtargets, cfg.ResolveConcurrency())

	if err := ctx.Err(); err != nil {
		return sum, err
	}

	entries := make([]manifest.Entry, 0, len(targets))
	for i, r := range results {
		e := manifest.Entry{
			URL:         r.URL,
			FinalURL:    r.FinalURL,
			LocalPath:   r.LocalPath,
			Size:        r.Size,
			SHA256:      r.SHA256,
			ContentType: r.ContentType,
			Kind:        targets[i].Kind,
			Status:      r.Status,
			Error:       r.Err,
			FetchedAt:   r.FetchedAt,
			FoundOn:     targets[i].FoundOn,
			Title:       targets[i].Title,
		}
		// Enrich HTML entries with the page's rating (and title fallback).
		if r.Status == manifest.StatusDownloaded && targets[i].Kind == manifest.KindHTML {
			if rating, title, err := readHTMLMeta(filepath.Join(cfg.OutputDir, r.LocalPath)); err == nil {
				e.Rating = rating
				if e.Title == "" && title != "" {
					e.Title = title
				}
			}
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

// discover runs the kind-gated discovery paths and returns the combined,
// deterministic download targets.
func discover(ctx context.Context, cfg config.Config, client *fetch.Client, logger *slog.Logger) ([]target, error) {
	var targets []target

	if cfg.HasKind(manifest.KindPDF) {
		cr, err := crawl.New(client, cfg.SeedURL, cfg.MaxDepth)
		if err != nil {
			return nil, err
		}
		logger.Info("discovering PDFs", "seed", cfg.SeedURL)
		links, err := cr.Discover(ctx)
		if err != nil {
			return nil, err
		}
		for _, l := range links {
			targets = append(targets, target{URL: l.URL, Kind: manifest.KindPDF, Title: l.Title, FoundOn: l.FoundOn})
		}
	}

	if cfg.HasKind(manifest.KindHTML) {
		cr, err := crawl.New(client, cfg.SeedURL, 0)
		if err != nil {
			return nil, err
		}
		logger.Info("discovering articles", "index", cfg.SeedURL)
		pages, err := cr.DiscoverArticles(ctx, cfg.SeedURL)
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			targets = append(targets, target{URL: p.URL, Kind: manifest.KindHTML, Title: p.Title, FoundOn: p.FoundOn})
		}
	}

	return targets, nil
}

// readHTMLMeta extracts the rating and title from a downloaded HTML file.
func readHTMLMeta(path string) (rating, title string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	res, err := htmltext.Extract(b)
	if err != nil {
		return "", "", err
	}
	return res.Rating, res.Title, nil
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
