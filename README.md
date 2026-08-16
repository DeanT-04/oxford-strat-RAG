<p align="center">
  <img src="docs/images/banner.png" alt="Vellum — Oxford Strat research RAG" width="100%">
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-B88746?style=flat&labelColor=002147" alt="License: MIT"></a>
  <a href="#"><img src="https://img.shields.io/badge/Go-1.26-B88746?style=flat&labelColor=002147" alt="Go 1.26"></a>
  <a href="#"><img src="https://img.shields.io/badge/CPU--only-no_GPU_required-B88746?style=flat&labelColor=002147" alt="CPU-only"></a>
  <a href="#"><img src="https://img.shields.io/badge/no_LLM_API_key-B88746?style=flat&labelColor=002147" alt="No LLM API key"></a>
  <a href="#"><img src="https://img.shields.io/badge/retrieval-BM25-B88746?style=flat&labelColor=002147" alt="BM25 retrieval"></a>
</p>

**Vellum** is an ultra-lightweight retrieval-augmented question answering
system over Oxford Capital Strategies' public-domain trading research. It
crawls the site and gathers **PDFs, HTML strategy reviews, thinker profiles,
the curated links directory, and TED talk transcripts**, indexes all of it,
and answers questions with cited evidence — all in one static Go binary, on a
laptop CPU, with no GPU and no paid LLM/embedding API.

The pipeline is three commands (plus two read-only listings):

```sh
vellum scrape   # gather PDFs, HTML, links, transcripts into data/
vellum ingest   # extract text from all of it and build the BM25 index
vellum query    # "your question" → ranked, cited chunks

vellum links    # list the curated external-links directory
vellum videos   # list talks and their transcript coverage
```

## Install

```sh
git clone https://github.com/DeanT-04/oxford-strat-RAG.git
cd oxford-strat-RAG
go build -o bin/vellum ./cmd/vellum
```

Requires Go 1.26+. Or install directly:

```sh
go install github.com/DeanT-04/oxford-strat-RAG/cmd/vellum@latest
```

## Usage

```sh
# 1. Gather everything (PDFs + HTML reviews + links + transcripts)
vellum scrape          # or -dry-run to preview

# 2. Extract text and build the index
vellum ingest

# 3. Ask questions
vellum query "value and momentum across asset classes"
vellum query -k 10 "how does the turtle trading system enter trades"
```

`scrape` gathers five content kinds (default `--kinds
pdf,html,links,video-text`):

| Kind | What it captures |
| --- | --- |
| `pdf` | every PDF in the article library — same-host uploads and external hosts (SSRN, CME, …), with a `reference` entry for paywalled/dead links |
| `html` | the ~100 strategy/indicator/data review pages (with their A–D rating) and the 4 thinker profile pages |
| `links` | the curated external-links directory → `data/links.json` |
| `video-text` | full transcripts of the TED talks, with speaker + talk title + source URL |

Scrape flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-url` | `https://oxfordstrat.com/resources/` | seed page to crawl |
| `-kinds` | `pdf,html,links,video-text` | content kinds to gather |
| `-articles` | `https://oxfordstrat.com/resources/articles/` | article-library index URL |
| `-out` | `data` | directory for downloaded files |
| `-concurrency` | `0` | download workers; `0` = auto (60% of CPUs) |
| `-depth` | `2` | same-host crawl depth |
| `-resume` | `false` | skip already-downloaded files |
| `-politeness` | `250ms` | minimum interval between requests |

Run `vellum <cmd> -h` for the complete flag list. Scrape flags can also be set
via `VELLUM_*` environment variables (flags win over env, env wins over
defaults).

## Query output

Each result is a BM25-ranked chunk with its source and citation:

```text
[1] 20.4838  Value and Momentum Everywhere
    source: ValMomEverywhere.pdf
    average Sharpe ratio across markets, indicating strong correlation structure …
[2] 19.8120  NR7 Pattern (Test: Setup & Exit)
    source: nr7.html
    url:    https://oxfordstrat.com/trading-strategies/nr7/
    the NR7 setup looks for the narrowest range of the last seven bars …
```

`data/manifest.json` records every item (URL, kind, local path, SHA-256, size,
status, rating, speaker); `data/index.json` is the queryable index.

## How it works

```mermaid
flowchart LR
    A[seed page] --> B{discover}
    B -->|pdf| C[BFS + article index]
    B -->|html| D[review + ideas pages]
    B -->|links| E[links.json]
    B -->|video-text| F[TED transcripts]
    C & D & F --> G[manifest.json]
    E --> G
    G --> H[extract text<br/>pdftotext / htmltext / transcript]
    H --> I[chunk]
    I --> J[BM25 index]
    J --> K[query → ranked, cited chunks]
```

- **Polite crawling** — browser User-Agent, timeouts, exponential backoff, a
  minimum interval between requests, and redirects to loopback/private hosts
  are refused.
- **Safe I/O** — sanitized filenames, path-containment guards, atomic writes.
- **Ultra-lightweight retrieval** — pure-Go BM25, in-memory, no GPU and no
  embedding service; query latency is microseconds for this corpus.
- **Grounded answers** — every chunk carries its source URL, so the host agent
  answers only from retrieved evidence.

Details: [ARCHITECTURE.md](ARCHITECTURE.md).

## Skill

A Reasonix skill (`/vellum-rag`, in `.reasonix/skills/vellum-rag/SKILL.md`)
wraps `vellum query` so the agent answers from retrieved chunks — no extra
LLM or embedding API key is required.

## Development

```sh
go vet ./...     # static analysis (must stay clean)
go test ./...    # unit tests (no network; httptest + stubs)
go test -cover ./...
```

Coverage is reported with
`go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out`.

## Known limitations

- External academic PDFs and SSRN papers are fetched from their original
  hosts; unreachable/paywalled ones are recorded as `reference` in the
  manifest rather than silently dropped.
- Modern TED transcripts carry no per-paragraph timestamps, so cited answers
  point to the talk + speaker rather than an exact moment.
- The one non-TED talk (CTA Masterclass) has no captions and is recorded as a
  `needs_transcript` reference; speech-to-text is not included.
- Three PDFs are image-only scans with no text layer and are skipped at
  ingest; they would need OCR (not included).
- Retrieval is lexical (BM25) — precise for exact terms but not semantic.
  Dense embeddings can be added behind the same `Extractor`/index seam later.

## License

[MIT](LICENSE) © 2026 DeanT-04
