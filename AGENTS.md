# AGENTS.md — Vellum (oxford-strat-RAG)

> This file is the **evolving system prompt** for every agent (human or AI)
> that works in this repository. It is a living document: when you learn
> something that changes how work should be done here, update this file and
> commit it in the same change. It is read before any non-trivial work.

## Project

**Vellum** is an ultra-lightweight research RAG for Oxford Capital
Strategies' public-domain trading literature (https://oxfordstrat.com).
It crawls the site, downloads every PDF, and indexes them so a user can ask
questions and get grounded, cited answers — without needing a GPU or any
paid LLM API.

- Repo: https://github.com/DeanT-04/oxford-strat-RAG
- License: MIT
- Language: **Go** (single static binary; 2 small deps — `x/net/html`,
  `ledongthuc/pdf`)
- Target hardware budget: **≤ 60% of CPU/memory** (AMD Ryzen 7 3700U, 14 GB
  RAM, integrated Vega 10 GPU). "Feels beefy" comes from smart retrieval,
  not raw compute.

## Commands

```sh
go build ./...          # compile everything
go vet ./...            # static analysis (must stay clean)
go test ./...           # run all tests
go test -cover ./...    # coverage summary
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
go run ./cmd/vellum scrape -dry-run -verbose   # discover-only dry run
go run ./cmd/vellum scrape                     # full download run
go run ./cmd/vellum ingest                     # extract text + build BM25 index
go run ./cmd/vellum query "question"           # ranked, cited chunks
go run ./cmd/vellum version
```

Binary name is `vellum`. Build a release binary with
`go build -ldflags "-s -w -X main.version=<tag>" -o bin/vellum ./cmd/vellum`.

## Layout & architecture

```
cmd/vellum/        thin CLI: flag parsing only; no logic
internal/config/   defaults + env + validation (no I/O besides injected getenv)
internal/fetch/    polite, retrying HTTP client (UA, backoff, size caps)
internal/crawl/    BFS discovery of PDF links (same-host, depth-limited)
internal/download/ worker pool: streaming, atomic writes, sha256, resume
internal/manifest/ JSON manifest (atomic write)
internal/text/     PDF -> text (pdftotext + pure-Go fallback) + tokenizer
internal/htmltext/ HTML -> text (article body + rating + title)
internal/chunk/    paragraph/sentence chunking with source metadata
internal/index/    BM25 index: build, search, JSON persistence
internal/ingest/   manifest -> text -> chunk -> index -> save
internal/query/    load index + render ranked, cited results
internal/app/      composition root wiring the scrape flow end-to-end
docs/              multi-language docs (EN + zh-CN)
```

Dependency direction is strict and acyclic; lower packages never import
higher ones. Interfaces are defined by the consumer (`crawl.Fetcher`,
`download.Fetcher`, `text.Extractor`) for testability.

## Coding standards

- **Errors**: wrap with `fmt.Errorf("context: %w", err)`. No panics in
  library code. Sentinel errors only where a caller must match them.
- **Context**: every function that can block takes `context.Context` first
  and honours cancellation.
- **Logging**: `log/slog` only (structured). Never log secrets or file
  contents. Verbose output is Info; default is Warn/Error.
- **Determinism**: sort discovered links; assign filenames up front so
  results are stable across runs.
- **Simplicity**: start with the simplest correct solution; add a building
  block only when the current design provably needs it. Standard library
  first; external deps are `golang.org/x/net/html` and
  `github.com/ledongthuc/pdf` (pure-Go PDF fallback).

## Security rules (non-negotiable)

- Validate every URL: scheme must be http/https; never follow file://.
- Never trust remote path segments: derive filenames from the URL basename,
  strip illegal chars, and guard with `safeJoin` (refuse `..` / separators).
- All HTTP requests get a timeout and a size cap; never buffer unbounded.
- Secrets come from the environment only; nothing hard-coded.
- Atomic writes (temp file + rename) for every artifact on disk.

## Testing policy

- Tests live next to code (`*_test.go`) using `testing` + `net/http/httptest`.
- Unit tests must **never touch the network**; use httptest or stubs.
- Strive for **100% coverage** on pure logic; keep orchestration near it.
- Table-driven tests preferred. Test like the user: run the real binary
  against the live site before marking a feature done.
- `go vet` and `go test ./...` must pass before every commit.

## Git workflow

- Conventional commit messages (`feat:`, `fix:`, `docs:`, `test:`, `chore:`).
- Small, atomic commits — one logical change each.
- Never commit `data/` (downloaded PDFs/manifests) — it is gitignored.
- Push to `main`; open a PR for anything a reviewer should see.

## Roadmap (build order)

1. ✅ Scrape: `vellum scrape` → `data/manifest.json`.
2. ✅ Ingest: `vellum ingest` → `data/index.json` (text → chunk → BM25).
3. ✅ Query: `vellum query "..."` → ranked chunks + citations.
4. ✅ Skill: `/vellum-rag` wraps the query CLI; the host agent answers from
   retrieved evidence — **no extra LLM API key, no Ollama**.
5. ✅ HTML strategy reviews: `vellum scrape --kinds pdf,html` also indexes the
   review articles (kind=html) with rating + source URL; `ingest` dispatches
   on kind.
6. ✅ Article library: `scrape` reads `/resources/articles/` as the
   authoritative corpus index — direct PDFs (same-host + external) are
   downloaded with host classification (`host` field) and scheme/www fallback,
   while paywalled/dead links (SSRN, store.traders.com) are recorded as
   `status: reference` so nothing is silently omitted.
7. ✅ Ideas profiles: the 4 thinker pages under `/resources/ideas/` are
   scraped as `kind: html` with a `person` key (kahneman, mandelbrot, popper,
   valen) and indexed alongside the strategy reviews.
8. ✅ Links directory: `/resources/links/` is captured to `data/links.json`
   (grouped external links + partner blurbs, never fetched/indexed) with a
   `kind: links` manifest pointer; `vellum links [group]` lists it.
9. Future: dense (semantic) embeddings behind the `Extractor`/index seam,
   OCR for scanned PDFs, `vellum serve` HTTP interface.

## How to evolve this file

When the project changes (new command, new invariant, a lesson learned), edit
the relevant section here and commit it with the change it documents. Keep it
short enough to actually be read: prefer rules over prose.
