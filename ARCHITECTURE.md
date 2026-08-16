# Vellum — Architecture

This document describes how Vellum is built. For what it does and how to run
it, see [README.md](README.md).

## Design goals

1. **Ultra-lightweight** — a single static Go binary; one external module
   (`golang.org/x/net/html`); no runtime services, no GPU.
2. **Ultra-precise** — deterministic output (sorted links, pre-assigned
   filenames, SHA-256 checksums), strict PDF validation, atomic writes.
3. **Bounded** — default concurrency is 60% of logical CPUs and every response
   is size-capped, so it never starves the host machine.
4. **Safe** — URL validation, sanitized filenames, path-containment guards,
   timeouts on all I/O, no secrets in code.

## Package layout

```
cmd/vellum/        CLI entry point. Flag parsing only — no logic.
internal/config/   Defaults, VELLUM_* env overlay, validation.
internal/fetch/    Polite, retrying HTTP client (UA, backoff, size caps).
internal/crawl/    Same-host BFS discovery of PDF links.
internal/download/ Bounded worker pool: streaming, atomic writes, sha256, resume.
internal/manifest/ JSON manifest (atomic write).
internal/app/      Composition root wiring the flow end-to-end.
```

Dependency direction is strict and acyclic:

```
app ──► config, fetch, crawl, download, manifest
```

Lower packages never import higher ones, and every package defines the
interface it needs from its collaborators (`crawl.Fetcher`, `download.Fetcher`)
so they stay unit-testable with stubs.

## Key decisions

### 60% CPU budget

`config.AutoConcurrency` computes `int(0.6 * NumCPU)`, floored at 1. The
downloader uses exactly that many workers. On the reference machine (Ryzen 7
3700U, 8 logical cores) that is 4 workers. Memory is bounded by a per-file
size cap and streaming copies (never buffering a whole PDF).

### Retry & politeness

`fetch.Client` enforces a minimum interval between requests (default 250 ms)
and retries transient failures — network errors, 408/429, and 5xx — with
exponential backoff. It never retries on cancellation or 4xx (except 408/429).
Every request carries a real User-Agent and a per-request timeout.

### Deterministic naming

`download.assignNames` maps every URL to a filename *before* any worker runs,
so results are stable regardless of goroutine scheduling. Collisions get a
short SHA-256 suffix. Filenames are sanitized (`sanitizeName`) from the URL
basename: illegal characters are replaced, a `.pdf` suffix is guaranteed, and
`safeJoin` refuses any name that could escape the output directory.

### Best-effort discovery

`crawl.Crawler` walks same-host HTML pages breadth-first up to a depth limit.
A failure on a non-seed page is skipped (the crawl continues); only a seed
failure is fatal. Off-host PDF links (e.g. academic hosts) are recorded but
never crawled into.

### Data contract

`manifest.json` is the machine-readable hand-off to the RAG phase. It records,
per discovered PDF: source URL, final (post-redirect) URL, local path, size,
SHA-256, content type, status (`downloaded` / `skipped` / `failed`), the page
it was found on, its anchor title, and fetch time.

## Roadmap

1. **Scraper** (shipped) — `vellum scrape` → `data/manifest.json`.
2. **Ingest** — extract text from the PDFs, chunk, and build a hybrid index
   (BM25 lexical + lightweight dense embeddings; still CPU-only).
3. **Query** — `vellum query "..."` returns ranked, cited chunks.
4. **Skill** — wrap the query CLI in a Reasonix skill so the host agent answers
   grounded in retrieved context, with no extra LLM API key or local model
   server required.
