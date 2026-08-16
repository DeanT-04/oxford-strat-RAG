# Vellum RAG

Answer questions against the local Oxford Capital Strategies research archive
using the `vellum` CLI's retrieval, and synthesize the answer from the
retrieved chunks. The language model that writes the final answer is the host
agent itself — no extra LLM or embedding API key is ever needed.

## Where the CLI lives

The repo is at `C:\Users\Deano\Documents\projects\oxford-strat-RAG`. Run query
commands from that directory. Prefer the prebuilt binary `./bin/vellum` when it
exists; otherwise use `go run ./cmd/vellum query`.

## Steps

1. Run retrieval with the user's question as the query (quote it to keep it one
   argument):
   `./bin/vellum query -k 5 "<question>"`
   Use `-k 10` for broad or multi-part questions.
2. Read the returned chunks. Each is ranked by BM25 score, shows the paper
   title, a `source:` PDF filename, and a text snippet.
3. Answer using ONLY what the chunks say. Attribute each claim to its source
   PDF filename. Prefer higher-ranked chunks.
4. If the top chunks don't actually answer the question, retry with a more
   specific query or a higher `-k`. If nothing is relevant, say the archive
   does not cover it — never fabricate an answer.

## Maintenance notes

- The archive indexes ~20 text PDFs from oxfordstrat.com. Three scanned PDFs
  (Bernoulli, donchian-20-guides, 10-Fallacies) have no text layer and are
  skipped by design.
- After re-scraping (`./bin/vellum scrape`), rebuild the index with
  `./bin/vellum ingest` before querying.
