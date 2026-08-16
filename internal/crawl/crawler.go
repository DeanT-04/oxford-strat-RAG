// Package crawl discovers PDF download links by breadth-first traversal of
// same-host pages. It never follows links off the seed host, but it records
// PDF links found anywhere (including external academic hosts).
package crawl

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Fetcher is the minimal HTTP surface the crawler needs. It is an interface
// so tests can supply an in-memory stub.
type Fetcher interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// PDFLink is a discovered PDF download target.
type PDFLink struct {
	URL     string // absolute, normalized URL of the PDF
	FoundOn string // page URL where the link was discovered
	Title   string // anchor text, if any
}

// Crawler walks same-host HTML pages up to a depth limit, collecting PDFs.
type Crawler struct {
	fetch    Fetcher
	seed     *url.URL
	host     string
	maxDepth int
}

// New builds a Crawler for the given seed URL and depth limit.
func New(f Fetcher, seedURL string, maxDepth int) (*Crawler, error) {
	u, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("crawl: parse seed: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("crawl: seed scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("crawl: seed has no host")
	}
	return &Crawler{
		fetch:    f,
		seed:     u,
		host:     normalizeHost(u.Host),
		maxDepth: maxDepth,
	}, nil
}

// Discover performs a breadth-first traversal beginning at the seed URL and
// returns the de-duplicated, deterministically ordered list of PDF links.
func (c *Crawler) Discover(ctx context.Context) ([]PDFLink, error) {
	type node struct {
		url   string
		depth int
	}

	seen := make(map[string]bool)
	pdfs := make(map[string]PDFLink)
	queue := []node{{url: c.seed.String(), depth: 0}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[cur.url] {
			continue
		}
		seen[cur.url] = true

		body, err := c.fetch.Get(ctx, cur.url)
		if err != nil {
			if cur.depth == 0 {
				return nil, fmt.Errorf("crawl: fetch seed %s: %w", cur.url, err)
			}
			continue // skip unreachable sub-pages, keep crawling
		}

		anchors, err := extractAnchors(pageBase(cur.url), body)
		if err != nil {
			if cur.depth == 0 {
				return nil, fmt.Errorf("crawl: parse seed %s: %w", cur.url, err)
			}
			continue
		}

		for _, a := range anchors {
			u, err := url.Parse(a.href)
			if err != nil {
				continue
			}
			if isPDFURL(u) {
				pdfs[u.String()] = PDFLink{
					URL:     u.String(),
					FoundOn: cur.url,
					Title:   a.text,
				}
				continue
			}
			// Only crawl further on the same host, within the depth limit.
			if cur.depth >= c.maxDepth {
				continue
			}
			if normalizeHost(u.Host) != c.host {
				continue
			}
			if isPageURL(u) {
				queue = append(queue, node{url: u.String(), depth: cur.depth + 1})
			}
		}
	}

	out := make([]PDFLink, 0, len(pdfs))
	for _, p := range pdfs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// anchor is a resolved link with its anchor text.
type anchor struct {
	href string
	text string
}

// extractAnchors parses HTML and returns every <a href> resolved against
// base, paired with its trimmed anchor text. It is pure and unit-testable.
func extractAnchors(base *url.URL, body []byte) ([]anchor, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	var anchors []anchor
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, text string
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					href = attr.Val
				}
			}
			text = strings.TrimSpace(linkText(n))
			if href != "" {
				if abs := resolveURL(base, href); abs != "" {
					anchors = append(anchors, anchor{href: abs, text: text})
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return anchors, nil
}

// linkText returns the concatenated text content of a node.
func linkText(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return sb.String()
}

// resolveURL resolves href against base and returns an absolute URL string,
// or "" when it cannot be resolved (e.g. non-http schemes like mailto:).
func resolveURL(base *url.URL, href string) string {
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	// Drop any fragment: it never matters for fetching.
	resolved.Fragment = ""
	return resolved.String()
}

// isPDFURL reports whether a URL's path ends in a (case-insensitive) .pdf.
func isPDFURL(u *url.URL) bool {
	return strings.HasSuffix(strings.ToLower(u.Path), ".pdf")
}

// isPageURL reports whether a URL looks like a navigable HTML page rather
// than an asset. Assets are skipped so we do not enqueue images/CSS/JS.
func isPageURL(u *url.URL) bool {
	p := strings.ToLower(u.Path)
	switch {
	case p == "":
		return true
	case strings.HasSuffix(p, "/"):
		return true
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".htm"),
		strings.HasSuffix(p, ".php"):
		return true
	default:
		// Extensionless paths (WordPress pretty permalinks) are pages.
		return !strings.Contains(strings.TrimPrefix(pathBase(p), ""), ".")
	}
}

// pathBase returns the final path segment.
func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// normalizeHost lower-cases a host and strips a trailing dot.
func normalizeHost(host string) string {
	h := strings.ToLower(host)
	return strings.TrimSuffix(h, ".")
}

// pageBase parses an absolute URL string into a base for resolving relative
// links. The input is always a validated seed or a resolveURL product, so
// parse failures are impossible in practice; an empty base is the fallback.
func pageBase(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{}
	}
	return u
}
