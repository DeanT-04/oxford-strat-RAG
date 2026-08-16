// Command vellum is the CLI for the Oxford Strat research tool. Its first
// subcommand, "scrape", crawls oxfordstrat.com and downloads every PDF.
// Later subcommands (ingest, query, serve) will build the RAG layer on top
// of the manifest produced here.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/app"
	"github.com/DeanT-04/oxford-strat-RAG/internal/config"
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

func usage(w io.Writer) {
	fmt.Fprint(w, `vellum — Oxford Strat research RAG

Usage:
  vellum scrape [flags]   crawl the site and download every PDF
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

Environment (overrides defaults, flags win):
  VELLUM_SEED_URL, VELLUM_OUTPUT_DIR, VELLUM_MANIFEST_PATH,
  VELLUM_CONCURRENCY, VELLUM_MAX_DEPTH, VELLUM_TIMEOUT, VELLUM_RETRIES,
  VELLUM_POLITENESS, VELLUM_MAX_FILE_SIZE, VELLUM_RESUME, VELLUM_DRY_RUN,
  VELLUM_VERBOSE, VELLUM_USER_AGENT
`)
}
