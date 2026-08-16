// Package crawl discovers PDF download links by breadth-first traversal of
// same-host pages. It never follows links off the seed host, but it records
// PDF links found anywhere (including external academic hosts).
package crawl

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
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

// PageLink is a discovered HTML page target (strategy review, profile page).
type PageLink struct {
	URL     string // absolute, normalized URL of the page
	FoundOn string // page URL where the link was discovered
	Title   string // anchor text, if any
	Person  string // profile-page person key (e.g. "kahneman"), if any
}

// ArticleLink is a paper link discovered on the article-library index.
type ArticleLink struct {
	URL   string
	Title string
	Host  string // HostOxfordstrat | HostExternal
	Kind  string // ArticlePDF | ArticleSSRN | ArticleReference
}

// Article link kinds.
const (
	ArticlePDF       = "pdf"
	ArticleSSRN      = "ssrn"
	ArticleReference = "reference"
)

// Host classifications for manifest entries.
const (
	HostOxfordstrat = "oxfordstrat"
	HostExternal    = "external"
)

// LinkItem is one curated external link from the links directory.
type LinkItem struct {
	Group string // group key (digital-libraries, research, …, partners)
	Name  string
	URL   string
	Blurb string // partner prose, if any
}

// linkGroupLabels maps a group key to the heading text it matches.
var linkGroupLabels = []struct{ key, label string }{
	{"digital-libraries", "Digital Libraries"},
	{"research", "Research"},
	{"cta-data", "CTA Data"},
	{"publications", "Publications"},
	{"exchanges", "Exchanges"},
	{"data", "Data"},
	{"partners", "Partners"},
}

// groupKeyFor returns the group key whose label appears in a heading, or "".
func groupKeyFor(heading string) string {
	h := normalizeHeading(heading)
	for _, g := range linkGroupLabels {
		if strings.Contains(h, strings.ToUpper(g.label)) {
			return g.key
		}
	}
	return ""
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

// articleHeadings are the substrings (matched case-insensitively) that mark
// the three review sections on the /resources/ index page.
var articleHeadings = []string{
	"trading strategies",
	"trading indicators",
	"data analysis",
}

// DiscoverArticles parses indexURL once and returns the same-host HTML page
// links listed under the review headings (strategy reviews, indicator
// reviews, data-analysis articles), deduplicated and sorted. It is the
// HTML/strategy-review analogue of Discover.
func (c *Crawler) DiscoverArticles(ctx context.Context, indexURL string) ([]PageLink, error) {
	body, err := c.fetch.Get(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("crawl: fetch articles index %s: %w", indexURL, err)
	}

	base := pageBase(indexURL)
	anchors, err := extractSectionLinks(base, body, articleHeadings)
	if err != nil {
		return nil, fmt.Errorf("crawl: parse articles index %s: %w", indexURL, err)
	}

	seen := make(map[string]PageLink)
	for _, a := range anchors {
		u, err := url.Parse(a.href)
		if err != nil {
			continue
		}
		if normalizeHost(u.Host) != c.host {
			continue // same-host only
		}
		if !isPageURL(u) {
			continue
		}
		if _, ok := seen[u.String()]; !ok {
			seen[u.String()] = PageLink{URL: u.String(), FoundOn: indexURL, Title: a.text}
		}
	}

	out := make([]PageLink, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// extractSectionLinks walks an HTML document and returns every <a href>
// resolved against base that appears under one of the given section headings,
// stopping at the next heading that does not match.
func extractSectionLinks(base *url.URL, body []byte, headings []string) ([]anchor, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	var links []anchor
	section := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Subtrees that never carry review links; also close any open
			// section so sidebar/nav/footer links are not collected.
			switch n.Data {
			case "script", "style", "nav", "header", "footer", "aside", "form":
				section = ""
				return
			}
			// Section headers are <hN> headings or (on the live site)
			// <strong> cells; rating grades are also <strong>, so only a
			// strong that matches a target heading opens a section — a
			// non-matching strong is ignored rather than closing it.
			if isHeading(n.Data) {
				if h := normalizeHeading(linkText(n)); h != "" {
					if matchesHeading(h, headings) {
						section = h
					} else {
						section = ""
					}
				}
			} else if n.Data == "strong" || n.Data == "b" {
				if h := normalizeHeading(linkText(n)); h != "" && matchesHeading(h, headings) {
					section = h
				}
			} else if n.Data == "a" && section != "" {
				var href string
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						href = attr.Val
					}
				}
				if href != "" {
					if abs := resolveURL(base, href); abs != "" {
						links = append(links, anchor{href: abs, text: strings.TrimSpace(linkText(n))})
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return links, nil
}

// DiscoverIdeas parses the ideas index once and returns the same-host
// profile page links (kind=html), deduplicated and sorted, with the person
// key derived from each slug. The index self-link and sidebar navigation are
// excluded.
func (c *Crawler) DiscoverIdeas(ctx context.Context, indexURL string) ([]PageLink, error) {
	body, err := c.fetch.Get(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("crawl: fetch ideas index %s: %w", indexURL, err)
	}
	base := pageBase(indexURL)
	anchors, err := extractAnchors(base, body)
	if err != nil {
		return nil, fmt.Errorf("crawl: parse ideas index %s: %w", indexURL, err)
	}

	seen := make(map[string]PageLink)
	for _, a := range anchors {
		u, err := url.Parse(a.href)
		if err != nil {
			continue
		}
		if normalizeHost(u.Host) != c.host {
			continue
		}
		// Only pages directly under the ideas index, excluding the index itself.
		if !strings.HasPrefix(u.Path, base.Path) || u.Path == base.Path {
			continue
		}
		if !isPageURL(u) {
			continue
		}
		if _, ok := seen[u.String()]; !ok {
			seen[u.String()] = PageLink{
				URL: u.String(), FoundOn: indexURL, Title: a.text,
				Person: personFromSlug(u.Path),
			}
		}
	}

	out := make([]PageLink, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// personFromSlug derives a person key from a profile slug's first segment
// (e.g. "kahneman-daniel" -> "kahneman").
func personFromSlug(p string) string {
	seg := pathBase(strings.TrimSuffix(p, "/"))
	if i := strings.Index(seg, "-"); i > 0 {
		seg = seg[:i]
	}
	return strings.ToLower(seg)
}

// DiscoverArticleLinks parses the article-library index once and returns
// every paper link it lists, deduplicated and sorted. Direct PDFs (same-host
// uploads and external hosts) are classified ArticlePDF; SSRN abstract pages
// are ArticleSSRN (to be resolved to a PDF or recorded as a reference);
// paywalled/store pages are ArticleReference.
func (c *Crawler) DiscoverArticleLinks(ctx context.Context, indexURL string) ([]ArticleLink, error) {
	body, err := c.fetch.Get(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("crawl: fetch articles index %s: %w", indexURL, err)
	}
	base := pageBase(indexURL)
	anchors, err := extractAnchors(base, body)
	if err != nil {
		return nil, fmt.Errorf("crawl: parse articles index %s: %w", indexURL, err)
	}

	seen := make(map[string]ArticleLink)
	for _, a := range anchors {
		u, err := url.Parse(a.href)
		if err != nil {
			continue
		}
		link, ok := classifyArticleLink(u, c.host, a.text)
		if !ok {
			continue
		}
		if _, dup := seen[link.URL]; !dup {
			seen[link.URL] = link
		}
	}

	out := make([]ArticleLink, 0, len(seen))
	for _, l := range seen {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// ResolveSSRNPDF fetches an SSRN abstract page and returns the direct PDF
// (delivery.php) URL it links to, if any.
func ResolveSSRNPDF(ctx context.Context, f Fetcher, abstractURL string) (string, error) {
	body, err := f.Get(ctx, abstractURL)
	if err != nil {
		return "", err
	}
	anchors, err := extractAnchors(pageBase(abstractURL), body)
	if err != nil {
		return "", err
	}
	for _, a := range anchors {
		if strings.Contains(strings.ToLower(a.href), "delivery.php") {
			return a.href, nil
		}
	}
	return "", fmt.Errorf("ssrn: no delivery link on %s", abstractURL)
}

// classifyArticleLink classifies a resolved link on the article index.
func classifyArticleLink(u *url.URL, selfHost, title string) (ArticleLink, bool) {
	host := normalizeHost(u.Host)
	switch host {
	case "twitter.com", "linkedin.com", "facebook.com", "youtube.com", "instagram.com":
		return ArticleLink{}, false // social/nav, not corpus content
	}
	hc := HostExternal
	if host == selfHost {
		hc = HostOxfordstrat
	}
	link := ArticleLink{URL: u.String(), Title: title, Host: hc}
	switch {
	case isPDFURL(u) || isPDFDownloadURL(u):
		link.Kind = ArticlePDF
	case host == "papers.ssrn.com" && strings.Contains(u.RawQuery, "abstract_id"):
		link.Kind = ArticleSSRN
	case host == "store.traders.com":
		link.Kind = ArticleReference
	default:
		return ArticleLink{}, false
	}
	return link, true
}

// isPDFDownloadURL reports whether a URL points at a PDF via a query flag
// (e.g. citeseerx "viewdoc/download?...type=pdf") rather than a .pdf suffix.
func isPDFDownloadURL(u *url.URL) bool {
	q := u.Query()
	return strings.EqualFold(q.Get("type"), "pdf") || strings.EqualFold(q.Get("format"), "pdf")
}

// DiscoverLinks parses the links directory once and returns the curated
// external links grouped by section, plus the partner blurbs. Nothing is
// fetched beyond the index page itself.
func (c *Crawler) DiscoverLinks(ctx context.Context, indexURL string) ([]LinkItem, error) {
	body, err := c.fetch.Get(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("crawl: fetch links index %s: %w", indexURL, err)
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("crawl: parse links index %s: %w", indexURL, err)
	}
	base := pageBase(indexURL)

	var items []LinkItem
	group := ""
	var partner *LinkItem

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "nav", "header", "footer", "aside", "form":
				return // skip boilerplate
			case "h1", "h2", "h3", "h4", "h5", "h6":
				if k := groupKeyFor(linkText(n)); k != "" {
					group = k
					partner = nil
				}
			case "strong":
				txt := strings.TrimSpace(linkText(n))
				if k := groupKeyFor(txt); k != "" {
					group = k
					partner = nil
				} else if group == "partners" && txt != "" {
					items = append(items, LinkItem{Group: "partners", Name: txt})
					partner = &items[len(items)-1]
				}
			case "tr":
				if group != "" && group != "partners" {
					if item, ok := rowItem(n, base); ok {
						item.Group = group
						items = append(items, item)
					}
				}
			}
		}
		if n.Type == html.TextNode && partner != nil {
			partner.Blurb += n.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Finalize partners: collapse the blurb and pull the URL from "Website: …".
	for i := range items {
		if items[i].Group != "partners" {
			continue
		}
		items[i].Blurb = strings.Join(strings.Fields(items[i].Blurb), " ")
		if u := websiteURL(items[i].Blurb); u != "" {
			items[i].URL = u
		}
	}
	return dedupeLinks(items), nil
}

// rowItem extracts a single name + URL from a links-directory table row.
func rowItem(tr *html.Node, base *url.URL) (LinkItem, bool) {
	var item LinkItem
	var href, name string
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "a" && href == "" {
				for _, a := range n.Attr {
					if a.Key == "href" {
						href = a.Val
					}
				}
			}
			if n.Data == "td" && name == "" {
				if t := strings.TrimSpace(linkText(n)); t != "" && !strings.HasPrefix(t, "http") {
					name = t
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(tr)
	if href == "" {
		return LinkItem{}, false
	}
	abs := resolveURL(base, href)
	if abs == "" {
		return LinkItem{}, false
	}
	item.URL = abs
	if name != "" {
		item.Name = name
	} else {
		item.Name = abs
	}
	return item, true
}

var websiteRe = regexp.MustCompile(`(?i)website:\s*(https?://[^\s]+)`)

// websiteURL extracts the partner's site URL from its blurb ("Website: …").
func websiteURL(blurb string) string {
	if m := websiteRe.FindStringSubmatch(blurb); len(m) == 2 {
		return strings.TrimSuffix(m[1], ".")
	}
	return ""
}

// dedupeLinks removes duplicate (group, name, url) entries, preserving order.
func dedupeLinks(items []LinkItem) []LinkItem {
	seen := make(map[string]bool)
	out := make([]LinkItem, 0, len(items))
	for _, it := range items {
		key := it.Group + "\x00" + it.Name + "\x00" + it.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

// isHeading reports whether tag is an HTML heading element.
func isHeading(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

// normalizeHeading upper-cases and collapses whitespace.
func normalizeHeading(s string) string {
	return strings.Join(strings.Fields(strings.ToUpper(s)), " ")
}

// matchesHeading reports whether a normalized heading contains any target.
func matchesHeading(h string, headings []string) bool {
	for _, t := range headings {
		if strings.Contains(h, strings.ToUpper(t)) {
			return true
		}
	}
	return false
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
