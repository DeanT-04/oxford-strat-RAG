package crawl

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// mapFetcher serves HTML pages from an in-memory map; unknown URLs error.
type mapFetcher struct {
	pages map[string]string
	err   error
}

func (m *mapFetcher) Get(ctx context.Context, u string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	if body, ok := m.pages[u]; ok {
		return []byte(body), nil
	}
	return nil, errors.New("not found: " + u)
}

func TestNew(t *testing.T) {
	if _, err := New(&mapFetcher{}, "ftp://x", 1); err == nil {
		t.Fatal("want error for non-http scheme")
	}
	if _, err := New(&mapFetcher{}, "https:///no-host", 1); err == nil {
		t.Fatal("want error for missing host")
	}
	c, err := New(&mapFetcher{}, "https://Example.com/", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.host != "example.com" {
		t.Fatalf("host = %q, want example.com", c.host)
	}
}

func TestResolveURL(t *testing.T) {
	base, _ := url.Parse("https://example.com/resources/")
	cases := []struct{ in, want string }{
		{"/resources/articles/", "https://example.com/resources/articles/"},
		{"articles/", "https://example.com/resources/articles/"},
		{"https://other.com/x.pdf", "https://other.com/x.pdf"},
		{"mailto:a@b.c", ""},
		{"", "https://example.com/resources/"},
		{"#frag", "https://example.com/resources/"},
	}
	for _, tc := range cases {
		if got := resolveURL(base, tc.in); got != tc.want {
			t.Errorf("resolveURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsPDFURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://x/a.pdf", true},
		{"https://x/a.PDF", true},
		{"https://x/a.pdf?dl=1", true},
		{"https://x/a.html", false},
		{"https://x/a", false},
	}
	for _, tc := range cases {
		u, _ := url.Parse(tc.in)
		if got := isPDFURL(u); got != tc.want {
			t.Errorf("isPDFURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsPageURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://x/", true},
		{"https://x/resources/articles/", true},
		{"https://x/a.html", true},
		{"https://x/a.php", true},
		{"https://x/a", true}, // extensionless pretty permalink
		{"https://x/a.png", false},
		{"https://x/style.css", false},
		{"https://x/a.js", false},
	}
	for _, tc := range cases {
		u, _ := url.Parse(tc.in)
		if got := isPageURL(u); got != tc.want {
			t.Errorf("isPageURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	if got := normalizeHost("Example.COM."); got != "example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractAnchors(t *testing.T) {
	base, _ := url.Parse("https://example.com/resources/")
	html := `<html><body>
		<a href="/resources/articles/">Articles</a>
		<a href="turtle.pdf">Turtle Rules</a>
		<a href="mailto:x@y.z">Email</a>
		<a href="https://other.com/x.pdf">External</a>
		<a href="//cdn.example.com/img.png">Img</a>
	</body></html>`
	anchors, err := extractAnchors(base, []byte(html))
	if err != nil {
		t.Fatal(err)
	}
	// mailto: is dropped; the rest resolve.
	if len(anchors) != 4 {
		t.Fatalf("len = %d, got %+v", len(anchors), anchors)
	}
	want := map[string]string{
		"https://example.com/resources/articles/":  "Articles",
		"https://example.com/resources/turtle.pdf": "Turtle Rules",
		"https://other.com/x.pdf":                  "External",
		"https://cdn.example.com/img.png":          "Img",
	}
	for _, a := range anchors {
		if want[a.href] != a.text {
			t.Errorf("anchor %q text = %q, want %q", a.href, a.text, want[a.href])
		}
	}
}

func TestDiscover(t *testing.T) {
	f := &mapFetcher{pages: map[string]string{
		"https://example.com/resources/": `
			<a href="/resources/articles/">articles</a>
			<a href="/resources/books/">books</a>
			<a href="/broken/page/">broken</a>
		`,
		"https://example.com/resources/articles/": `
			<a href="turtle.pdf">Turtle</a>
			<a href="https://external.edu/paper.pdf">External Paper</a>
			<a href="chart.png">chart</a>
		`,
		"https://example.com/resources/books/": `
			<a href="book.pdf">Book</a>
		`,
	}}
	c, err := New(f, "https://example.com/resources/", 2)
	if err != nil {
		t.Fatal(err)
	}
	links, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 3 {
		t.Fatalf("len = %d, links = %+v", len(links), links)
	}
	// Sorted by URL.
	wantOrder := []string{
		"https://example.com/resources/articles/turtle.pdf",
		"https://example.com/resources/books/book.pdf",
		"https://external.edu/paper.pdf",
	}
	for i, w := range wantOrder {
		if links[i].URL != w {
			t.Errorf("links[%d] = %s, want %s", i, links[i].URL, w)
		}
	}
	if links[0].FoundOn != "https://example.com/resources/articles/" {
		t.Errorf("FoundOn = %s", links[0].FoundOn)
	}
	if links[0].Title != "Turtle" {
		t.Errorf("Title = %q", links[0].Title)
	}
}

func TestDiscoverSeedFailure(t *testing.T) {
	f := &mapFetcher{err: errors.New("boom")}
	c, _ := New(f, "https://example.com/", 1)
	if _, err := c.Discover(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverCanceled(t *testing.T) {
	f := &mapFetcher{pages: map[string]string{
		"https://example.com/": `<a href="a.pdf">a</a>`,
	}}
	c, _ := New(f, "https://example.com/", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Discover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestNewParseError(t *testing.T) {
	if _, err := New(&mapFetcher{}, "://bad", 1); err == nil {
		t.Fatal("want error for unparseable seed")
	}
}

func TestDiscoverDepthLimit(t *testing.T) {
	f := &mapFetcher{pages: map[string]string{
		"https://example.com/":      `<a href="/docs/">docs</a>`,
		"https://example.com/docs/": `<a href="x.pdf">x</a>`,
	}}
	c, _ := New(f, "https://example.com/", 0)
	links, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("depth 0 must not follow sub-pages, got %+v", links)
	}
}

func TestDiscoverCycleAndExternal(t *testing.T) {
	f := &mapFetcher{pages: map[string]string{
		"https://example.com/": `
			<a href="/">self</a>
			<a href="https://other.com/page.html">external page</a>
			<a href="/docs/">docs</a>
		`,
		"https://example.com/docs/": `<a href="doc.pdf">doc</a>`,
	}}
	c, _ := New(f, "https://example.com/", 2)
	links, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].URL != "https://example.com/docs/doc.pdf" {
		t.Fatalf("links = %+v", links)
	}
}

func TestResolveURLInvalidHref(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	if got := resolveURL(base, "/%zz"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestIsPageURLExtra(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://x", true},       // empty path
		{"https://x/a.htm", true}, // .htm suffix
	}
	for _, tc := range cases {
		u, _ := url.Parse(tc.in)
		if got := isPageURL(u); got != tc.want {
			t.Errorf("isPageURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPathBase(t *testing.T) {
	if got := pathBase("abc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := pathBase("/a/b/c"); got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestPageBase(t *testing.T) {
	if got := pageBase("https://example.com/x"); got.Host != "example.com" {
		t.Fatalf("host = %q", got.Host)
	}
	if got := pageBase("%zz"); got.Host != "" {
		t.Fatalf("fallback should have empty host, got %q", got.Host)
	}
}

func TestDiscoverArticles(t *testing.T) {
	f := &mapFetcher{pages: map[string]string{
		"https://example.com/resources/": `
			<nav><a href="/about/">About</a></nav>
			<h2>Reviews of Trading Strategies (Public Domain)</h2>
			<ul>
				<li><a href="/trading-strategies/nr7/">NR7 Pattern</a></li>
				<li><a href="/ideas/toby-crabel-narrow-range-1/">Toby Crabel</a></li>
				<li><a href="/trading-strategies/nr7/">NR7 duplicate</a></li>
				<li><a href="chart.png">chart</a></li>
			</ul>
			<h2>Reviews of Trading Indicators</h2>
			<a href="/indicators/macd/">MACD</a>
			<h2>Data Analysis</h2>
			<a href="/data/equity-curve/">Equity Curve</a>
			<h2>Blog</h2>
			<a href="/blog/popper-induction/">popper</a>
		`,
	}}
	c, err := New(f, "https://example.com/resources/", 0)
	if err != nil {
		t.Fatal(err)
	}
	links, err := c.DiscoverArticles(context.Background(), "https://example.com/resources/")
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, l := range links {
		urls = append(urls, l.URL)
	}
	want := []string{
		"https://example.com/data/equity-curve/",
		"https://example.com/ideas/toby-crabel-narrow-range-1/",
		"https://example.com/indicators/macd/",
		"https://example.com/trading-strategies/nr7/",
	}
	if len(urls) != len(want) {
		t.Fatalf("got %d links %v, want %d", len(urls), urls, len(want))
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Errorf("links[%d] = %q, want %q", i, urls[i], want[i])
		}
	}
	// The external/nav links must be excluded; duplicates deduped.
	if links[3].Title != "NR7 Pattern" {
		t.Fatalf("title = %q", links[3].Title)
	}
}

func TestDiscoverArticlesSeedFailure(t *testing.T) {
	f := &mapFetcher{err: errors.New("boom")}
	c, _ := New(f, "https://example.com/", 0)
	if _, err := c.DiscoverArticles(context.Background(), "https://example.com/"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractSectionLinksExternalExcluded(t *testing.T) {
	base, _ := url.Parse("https://example.com/resources/")
	body := []byte(`<h2>Data Analysis</h2>
		<a href="/data/a/">local</a>
		<a href="https://other.com/x/">external</a>`)
	anchors, err := extractSectionLinks(base, body, articleHeadings)
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("len = %d", len(anchors))
	}
}

func TestExtractSectionLinksStrongHeaders(t *testing.T) {
	base, _ := url.Parse("https://example.com/resources/")
	// Mirrors the live layout: section headers are <strong> table cells and
	// the rating grade is a <strong> inside a link row; the sidebar nav lives
	// in an <aside> and must be excluded.
	body := []byte(`
		<table><tr><td><strong>REVIEWS OF TRADING STRATEGIES (PUBLIC DOMAIN)</strong></td></tr>
		<tr><td><a href="/trading-strategies/nr7/">NR7 Pattern</a> | Rating: A/B/<strong>C</strong>/D</td></tr>
		<tr><td><strong>DATA ANALYSIS</strong></td></tr>
		<tr><td><a href="/data/global-market-correlations/">Global Market Correlations</a></td></tr>
		</table>
		<aside><ul><li><a href="/resources/articles/">Articles</a></li>
		<li><a href="/resources/ideas/">Ideas</a></li></ul></aside>`)
	anchors, err := extractSectionLinks(base, body, articleHeadings)
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, a := range anchors {
		urls = append(urls, a.href)
	}
	want := []string{
		"https://example.com/trading-strategies/nr7/",
		"https://example.com/data/global-market-correlations/",
	}
	if len(urls) != len(want) {
		t.Fatalf("urls = %v, want %v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Errorf("urls[%d] = %q, want %q", i, urls[i], want[i])
		}
	}
}
