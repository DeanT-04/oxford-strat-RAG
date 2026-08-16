// Command vellum is the CLI for the Oxford Strat research tool.
//
//	vellum scrape   crawl oxfordstrat.com and download every PDF
//	vellum ingest   extract text from PDFs and build a BM25 index
//	vellum query    search the index and return cited chunks
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/app"
	"github.com/DeanT-04/oxford-strat-RAG/internal/chunk"
	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
	"github.com/DeanT-04/oxford-strat-RAG/internal/ingest"
	"github.com/DeanT-04/oxford-strat-RAG/internal/links"
	"github.com/DeanT-04/oxford-strat-RAG/internal/manifest"
	"github.com/DeanT-04/oxford-strat-RAG/internal/query"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the top-level command and returns a process exit code. It is
// separated from main so it can be exercised by tests.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 0
	}
	switch args[0] {
	case "scrape":
		return runScrape(args[1:], stdout, stderr)
	case "ingest":
		return runIngest(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	case "links":
		return runLinks(args[1:], stdout, stderr)
	case "videos":
		return runVideos(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "vellum %s\n", version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func runScrape(args []string, stdout, stderr io.Writer) int {
	// Layer config: defaults <- environment <- command-line flags.
	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 2
	}

	fs := flag.NewFlagSet("scrape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.SeedURL, "url", cfg.SeedURL, "seed URL to crawl")
	fs.StringVar(&cfg.OutputDir, "out", cfg.OutputDir, "output directory for downloads")
	fs.StringVar(&cfg.ManifestPath, "manifest", cfg.ManifestPath, "path of the JSON manifest")
	fs.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "download workers (0 = auto 60% of CPUs)")
	fs.IntVar(&cfg.MaxDepth, "depth", cfg.MaxDepth, "same-host crawl depth")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request timeout")
	fs.IntVar(&cfg.Retries, "retries", cfg.Retries, "retries after the first attempt")
	fs.DurationVar(&cfg.Politeness, "politeness", cfg.Politeness, "minimum interval between requests")
	fs.Int64Var(&cfg.MaxFileSize, "max-size", cfg.MaxFileSize, "maximum file size in bytes")
	fs.StringVar(&cfg.UserAgent, "user-agent", cfg.UserAgent, "User-Agent header")
	fs.BoolVar(&cfg.Resume, "resume", cfg.Resume, "skip files that already exist")
	fs.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "discover and report only, download nothing")
	fs.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "verbose logging")
	fs.StringVar(&cfg.Kinds, "kinds", cfg.Kinds, "comma-separated content kinds to gather (pdf,html,…)")
	fs.StringVar(&cfg.ArticlesURL, "articles", cfg.ArticlesURL, "article-library index URL")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	start := time.Now()
	sum, err := app.Run(ctx, cfg, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if cfg.Verbose {
		fmt.Fprintf(stdout, "done in %s (workers: %d)\n",
			time.Since(start).Round(time.Millisecond), cfg.ResolveConcurrency())
	}
	_ = sum
	return 0
}

func runIngest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		manifestPath = fs.String("manifest", "data/manifest.json", "manifest path")
		dataDir      = fs.String("dir", "", "directory containing downloaded PDFs (default: manifest dir)")
		indexPath    = fs.String("index", "data/index.json", "index output path")
		maxChunk     = fs.Int("max-chunk", chunk.DefaultMaxLen, "max chunk size in characters")
		minChunk     = fs.Int("min-chunk", chunk.DefaultMinLen, "min chunk size in characters")
		verbose      = fs.Bool("verbose", false, "verbose logging")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	sum, err := ingest.Run(ingest.Options{
		ManifestPath: *manifestPath,
		DataDir:      *dataDir,
		IndexPath:    *indexPath,
		MaxChunk:     *maxChunk,
		MinChunk:     *minChunk,
		Verbose:      *verbose,
	}, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	for _, r := range sum.Results {
		fmt.Fprintf(stdout, "%-9s %5d  %s\n", r.Status, r.Chunks, r.Source)
	}
	fmt.Fprintf(stdout, "docs: %d, indexed: %d, no_text: %d, failed: %d, chunks: %d\n",
		sum.Docs, sum.Indexed, sum.NoText, sum.Failed, sum.Chunks)
	fmt.Fprintf(stdout, "index: %s\n", sum.IndexPath)
	return 0
}

func runLinks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("links", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("file", "data/links.json", "links JSON path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	group := strings.Join(fs.Args(), " ")
	doc, err := links.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	doc.List(group, stdout)
	return 0
}

func runVideos(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("videos", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifestPath := fs.String("manifest", "data/manifest.json", "manifest path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	m, err := manifest.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	for _, e := range m.Entries {
		switch e.Kind {
		case manifest.KindVideoText, manifest.KindVideo:
			status := "indexed"
			if e.Status != manifest.StatusDownloaded {
				status = "reference"
			}
			fmt.Fprintf(stdout, "%-9s %-28s %s\n", status, e.Speaker, e.Title)
			if e.URL != "" {
				fmt.Fprintf(stdout, "          %s\n", e.URL)
			}
		}
	}
	return 0
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	indexPath := fs.String("index", "data/index.json", "index path")
	k := fs.Int("k", 5, "number of results to return")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	q := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(q) == "" {
		fmt.Fprintln(stderr, "usage: vellum query [-k N] \"your question\"")
		return 2
	}
	if err := query.Run(*indexPath, q, *k, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `vellum — Oxford Strat research RAG

Usage:
  vellum scrape [flags]   crawl the site and download every PDF
  vellum ingest [flags]   extract text from PDFs and build a BM25 index
  vellum query [flags] Q  search the index and return cited chunks
  vellum links [group]    list the curated external-links directory
  vellum videos           list video talks and their transcript coverage
  vellum version          print the version
  vellum help             show this help

Scrape flags:
  -url          seed URL to crawl          (default https://oxfordstrat.com/resources/)
  -out          output directory            (default data)
  -manifest     JSON manifest path          (default data/manifest.json)
  -concurrency  download workers, 0 = auto  (default 0 = 60% of CPUs)
  -depth        same-host crawl depth       (default 2)
  -timeout      per-request timeout         (default 30s)
  -retries      retries per request         (default 3)
  -politeness   min interval between reqs   (default 250ms)
  -max-size     max file size in bytes      (default 536870912)
  -resume       skip already-downloaded files
  -dry-run      discover only, no downloads
  -verbose      verbose logging
  -kinds        kinds to gather             (default pdf,html,links,video-text)
  -articles     article-library index URL

Environment (overrides defaults, flags win):
  VELLUM_SEED_URL, VELLUM_OUTPUT_DIR, VELLUM_MANIFEST_PATH,
  VELLUM_CONCURRENCY, VELLUM_MAX_DEPTH, VELLUM_TIMEOUT, VELLUM_RETRIES,
  VELLUM_POLITENESS, VELLUM_MAX_FILE_SIZE, VELLUM_RESUME, VELLUM_DRY_RUN,
  VELLUM_VERBOSE, VELLUM_USER_AGENT, VELLUM_KINDS, VELLUM_ARTICLES_URL

Ingest flags:
  -manifest  JSON manifest path   (default data/manifest.json)
  -dir       PDF directory        (default: manifest dir)
  -index     index output path    (default data/index.json)
  -max-chunk max chunk chars      (default 1200)
  -min-chunk min chunk chars      (default 80)

Query flags:
  -index  index path              (default data/index.json)
  -k      number of results       (default 5)
`)
}
