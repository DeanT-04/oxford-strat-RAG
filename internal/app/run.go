// Package app wires config, fetching, crawling, downloading, and manifest
// writing into one runnable flow. It is the composition root; cmd/vellum is
// only a thin flag parser around it.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
	"github.com/DeanT-04/oxford-strat-RAG/internal/crawl"
	"github.com/DeanT-04/oxford-strat-RAG/internal/download"
	"github.com/DeanT-04/oxford-strat-RAG/internal/fetch"
	"github.com/DeanT-04/oxford-strat-RAG/internal/htmltext"
	"github.com/DeanT-04/oxford-strat-RAG/internal/links"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
)

// Summary reports the outcome of a run.
type Summary struct {
	Discovered   int
	Downloaded   int
	Skipped      int
	Failed       int
	Referenced   int
	ManifestPath string
}

// target is a discovered download candidate with its metadata. Reference
// targets are documented in the manifest but never downloaded.
type target struct {
	URL       string
	Kind      string // manifest.KindPDF | manifest.KindHTML | crawl.ArticleSSRN
	Title     string
	FoundOn   string
	Host      string
	Person    string
	Reference bool
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

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return sum, fmt.Errorf("create output dir: %w", err)
	}

	entries := make([]manifest.Entry, 0, len(targets)+1)

	// The links directory is a curated pointer list, captured for completeness
	// even when no downloadable content is found.
	if linkEntry, err := captureLinks(ctx, client, cfg); err == nil {
		entries = append(entries, *linkEntry)
		sum.Referenced++
	} else {
		logger.Warn("links capture failed", "err", err)
	}

	if len(targets) == 0 {
		m := manifest.New(cfg.SeedURL, entries)
		if err := m.WriteFile(cfg.ManifestPath); err != nil {
			return sum, err
		}
		sum.ManifestPath = cfg.ManifestPath
		fmt.Fprintln(stdout, "no content links discovered")
		fmt.Fprintf(stdout, "manifest: %s\n", cfg.ManifestPath)
		return sum, nil
	}

	downloadable, references := resolveTargets(ctx, client, targets)

	for _, t := range references {
		entries = append(entries, manifest.Entry{
			URL:       t.URL,
			Kind:      manifest.KindPDF,
			Status:    manifest.StatusReference,
			Host:      t.Host,
			Title:     t.Title,
			FoundOn:   t.FoundOn,
			FetchedAt: time.Now(),
		})
		sum.Referenced++
	}

	dtargets := make([]download.Target, len(downloadable))
	for i, t := range downloadable {
		dtargets[i] = download.Target{URL: t.URL, Kind: t.Kind}
	}
	pool := download.New(client, cfg.OutputDir, cfg.MaxFileSize, cfg.Resume)
	results := pool.DownloadTargets(ctx, dtargets, cfg.ResolveConcurrency())

	if err := ctx.Err(); err != nil {
		return sum, err
	}

	for i, r := range results {
		e := manifest.Entry{
			URL:         r.URL,
			FinalURL:    r.FinalURL,
			LocalPath:   r.LocalPath,
			Size:        r.Size,
			SHA256:      r.SHA256,
			ContentType: r.ContentType,
			Kind:        downloadable[i].Kind,
			Status:      r.Status,
			Error:       r.Err,
			FetchedAt:   r.FetchedAt,
			FoundOn:     downloadable[i].FoundOn,
			Title:       downloadable[i].Title,
			Host:        downloadable[i].Host,
			Person:      downloadable[i].Person,
		}
		// Enrich HTML entries with the page's rating (and title fallback).
		if r.Status == manifest.StatusDownloaded && downloadable[i].Kind == manifest.KindHTML {
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

	// Deterministic output regardless of discovery/download ordering.
	sort.Slice(entries, func(i, j int) bool { return entries[i].URL < entries[j].URL })

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
		"referenced", sum.Referenced,
	)
	fmt.Fprintf(stdout, "discovered: %d, downloaded: %d, skipped: %d, failed: %d, referenced: %d\n",
		sum.Discovered, sum.Downloaded, sum.Skipped, sum.Failed, sum.Referenced)
	fmt.Fprintf(stdout, "manifest: %s\n", cfg.ManifestPath)
	return sum, nil
}

// discover runs the kind-gated discovery paths and returns the combined,
// deterministic download targets.
func discover(ctx context.Context, cfg config.Config, client *fetch.Client, logger *slog.Logger) ([]target, error) {
	selfHost := selfHostOf(cfg.SeedURL)
	var targets []target
	seen := make(map[string]bool)
	add := func(t target) {
		if seen[t.URL] {
			return
		}
		seen[t.URL] = true
		targets = append(targets, t)
	}

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
			add(target{URL: l.URL, Kind: manifest.KindPDF, Title: l.Title, FoundOn: l.FoundOn, Host: hostOf(l.URL, selfHost)})
		}

		// The article-library index lists the full corpus, including external
		// papers (SSRN, CME, citeseerx, …) the BFS never sees.
		logger.Info("discovering article links", "index", cfg.ArticlesURL)
		alinks, err := cr.DiscoverArticleLinks(ctx, cfg.ArticlesURL)
		if err != nil {
			return nil, err
		}
		for _, a := range alinks {
			switch a.Kind {
			case crawl.ArticlePDF:
				add(target{URL: a.URL, Kind: manifest.KindPDF, Title: a.Title, FoundOn: cfg.ArticlesURL, Host: a.Host})
			case crawl.ArticleSSRN:
				add(target{URL: a.URL, Kind: crawl.ArticleSSRN, Title: a.Title, FoundOn: cfg.ArticlesURL, Host: a.Host})
			case crawl.ArticleReference:
				add(target{URL: a.URL, Kind: manifest.KindPDF, Title: a.Title, FoundOn: cfg.ArticlesURL, Host: a.Host, Reference: true})
			}
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
			add(target{URL: p.URL, Kind: manifest.KindHTML, Title: p.Title, FoundOn: p.FoundOn, Host: selfHost})
		}

		// The ideas (thinker profile) index lives beside the seed.
		ideasURL := strings.TrimSuffix(cfg.SeedURL, "/") + "/ideas/"
		logger.Info("discovering ideas", "index", ideasURL)
		ideas, err := cr.DiscoverIdeas(ctx, ideasURL)
		if err != nil {
			return nil, err
		}
		for _, p := range ideas {
			add(target{URL: p.URL, Kind: manifest.KindHTML, Title: p.Title, FoundOn: p.FoundOn, Host: selfHost, Person: p.Person})
		}
	}

	return targets, nil
}

// resolveTargets splits targets into downloadable jobs and reference entries.
// SSRN abstract links are resolved to their delivery PDF URL here; if that
// fails they become references so the corpus stays documented, never silent.
func resolveTargets(ctx context.Context, f crawl.Fetcher, targets []target) (downloadable, references []target) {
	for _, t := range targets {
		if t.Reference {
			references = append(references, t)
			continue
		}
		if t.Kind == crawl.ArticleSSRN {
			pdfURL, err := crawl.ResolveSSRNPDF(ctx, f, t.URL)
			if err != nil {
				t.Reference = true
				t.Kind = manifest.KindPDF
				references = append(references, t)
				continue
			}
			t.URL = pdfURL
			t.Kind = manifest.KindPDF
		}
		downloadable = append(downloadable, t)
	}
	return downloadable, references
}

// captureLinks fetches the links directory and writes data/links.json,
// returning the manifest pointer entry for it.
func captureLinks(ctx context.Context, client *fetch.Client, cfg config.Config) (*manifest.Entry, error) {
	linksURL := strings.TrimSuffix(cfg.SeedURL, "/") + "/links/"
	cr, err := crawl.New(client, cfg.SeedURL, 0)
	if err != nil {
		return nil, err
	}
	items, err := cr.DiscoverLinks(ctx, linksURL)
	if err != nil {
		return nil, err
	}
	groups := make(map[string][]links.Item)
	for _, it := range items {
		groups[it.Group] = append(groups[it.Group], links.Item{Name: it.Name, URL: it.URL, Blurb: it.Blurb})
	}
	doc := links.New(linksURL, groups)
	if err := doc.WriteFile(filepath.Join(cfg.OutputDir, "links.json")); err != nil {
		return nil, err
	}
	return &manifest.Entry{
		URL:       linksURL,
		Kind:      manifest.KindLinks,
		Status:    manifest.StatusReference,
		Host:      hostOf(linksURL, selfHostOf(cfg.SeedURL)),
		Title:     "External links directory",
		FoundOn:   cfg.SeedURL,
		FetchedAt: time.Now(),
	}, nil
}

// hostOf classifies a URL as oxfordstrat (same host as selfHost) or external.
func hostOf(rawURL, selfHost string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return crawl.HostExternal
	}
	if strings.EqualFold(strings.TrimSuffix(u.Host, "."), selfHost) {
		return crawl.HostOxfordstrat
	}
	return crawl.HostExternal
}

// selfHostOf returns the normalized host of the seed URL.
func selfHostOf(seedURL string) string {
	u, err := url.Parse(seedURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(u.Host, "."))
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
