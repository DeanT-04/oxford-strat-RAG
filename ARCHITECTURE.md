# Vellum — Architecture

This document describes how Vellum is built. For what it does and how to run
it, see [README.md](README.md).

## Design goals

1. **Ultra-lightweight** — a single static Go binary; two small modules
   (`golang.org/x/net/html`, `github.com/ledongthuc/pdf`); no runtime services,
   no GPU, no embedding model server.
2. **Ultra-precise** — deterministic output (sorted links, pre-assigned
   filenames, SHA-256 checksums), strict PDF validation, atomic writes, and
   retrieval that returns the source PDF for every chunk.
3. **Bounded** — default concurrency is 60% of logical CPUs and every response
   is size-capped.
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
internal/text/     PDF → text extraction (pdftotext + pure-Go fallback) + tokenizer.
internal/chunk/    Paragraph/sentence chunking with source metadata.
internal/index/    BM25 index: build, search, JSON persistence.
internal/ingest/   Orchestrates manifest → text → chunk → index → save.
internal/query/    Loads the index and renders ranked, cited results.
internal/app/      Composition root for the scrape pipeline.
```

Dependency direction is strict and acyclic; lower packages never import higher
ones, and each package defines the interface it needs from its collaborators
(`crawl.Fetcher`, `download.Fetcher`, `text.Extractor`) for testability.

## Key decisions

### 60% CPU budget

`config.AutoConcurrency` computes `int(0.6 * NumCPU)`, floored at 1. The
downloader uses exactly that many workers. Memory is bounded by a per-file
size cap and streaming copies.

### Retry & politeness

`fetch.Client` enforces a minimum interval between requests and retries
transient failures (network errors, 408/429, 5xx) with exponential backoff.
It never retries on cancellation or non-transient 4xx.

### Deterministic naming

`download.assignNames` maps URLs to filenames *before* any worker runs, so
results are stable regardless of goroutine scheduling. Collisions get a short
SHA-256 suffix; `sanitizeName` strips illegal characters and `safeJoin`
refuses any name that could escape the output directory.

### Text extraction (pluggable)

`text.Default()` prefers the poppler `pdftotext` binary when present (highest
fidelity for academic PDFs) and falls back to a pure-Go parser otherwise. The
choice is an `Extractor` interface, so OCR or a different backend can be
swapped in later. PDFs with no extractable text (image scans) are skipped and
recorded as `no_text`.

### Retrieval (BM25)

`index.Build` tokenizes each chunk and constructs an inverted index with
per-term postings and document lengths. `index.Search` scores chunks with
BM25 (k1=1.5, b=0.75) using the shared tokenizer, so documents and queries are
tokenized identically. For a corpus of ~20 papers the whole index lives in
memory and queries complete in microseconds. The tokenizer keeps trading terms
and drops a small English stopword list.

### Data contract

- `data/manifest.json` — per-PDF download outcome (URL, final URL, local path,
  size, SHA-256, content type, status, found-on page, title, fetch time).
- `data/index.json` — the BM25 index: all chunks (with source metadata) plus
  postings and document lengths, written atomically.

## Roadmap

1. ✅ **Scrape** — `vellum scrape` → `data/manifest.json`.
2. ✅ **Ingest** — `vellum ingest` → `data/index.json` (text → chunk → BM25).
3. ✅ **Query** — `vellum query "..."` → ranked, cited chunks.
4. ✅ **Skill** — `/vellum-rag` wraps `vellum query`; the host agent answers
   from retrieved evidence with no extra LLM/embedding API key.
5. **Future** — dense (semantic) embeddings behind the `Extractor`/index seam,
   OCR for scanned PDFs, and a `vellum serve` HTTP interface.
