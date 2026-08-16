// Package htmltext extracts readable article text and metadata from HTML
// pages. It is the HTML analogue of internal/text: same role for the crawl
// pipeline, but for the strategy-review, ideas, and profile pages rather
// than PDFs. It uses golang.org/x/net/html, which is already a dependency.
package htmltext

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Result is the output of Extract.
type Result struct {
	Text   string // readable body text, boilerplate stripped
	Rating string // "A".."D" when the page carries a strategy rating, else ""
	Title  string // page <title>, trimmed
}

// skipTags are whole subtrees that never contribute body text. Note: "header"
// is deliberately absent — some WordPress templates emit an unclosed <header>,
// so skipping it would swallow the entire article body.
var skipTags = map[string]bool{
	"script": true, "style": true, "nav": true,
	"footer": true, "form": true, "noscript": true, "iframe": true,
	"svg": true, "aside": true,
}

// blockTags are elements after which we emit a newline so headings, list
// items, table rows, and paragraphs stay on their own lines.
var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "main": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ul": true, "ol": true, "br": true, "blockquote": true,
	"tr": true, "table": true, "pre": true, "hr": true, "figure": true,
}

// boilerplate substrings (lower-cased) dropped from every page. Kept small
// and exact so real content is never discarded.
var boilerplate = []string{
	"cftc rule 4.41",
	"sign up to receive research news",
	"disclaimer: this website",
}

var ratingRe = regexp.MustCompile(`(?i)rating\s*[:：]\s*([a-d])\b`)

// ratingLabelRe matches the "Rating:" label the site renders before a grade.
var ratingLabelRe = regexp.MustCompile(`(?i)rating\s*[:：]`)

// Extract parses HTML and returns the readable text plus rating and title.
func Extract(body []byte) (Result, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("htmltext: parse: %w", err)
	}

	var sb strings.Builder
	walkText(doc, &sb)
	raw := sb.String()

	return Result{
		Text:   clean(raw),
		Rating: extractRating(doc, raw),
		Title:  findTitle(doc),
	}, nil
}

// walkText emits visible text with structural markers: a tab before table
// cells and a newline after block-level elements.
func walkText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.ElementNode {
		if skipTags[n.Data] {
			return
		}
		if n.Data == "td" || n.Data == "th" {
			sb.WriteByte('\t')
		}
	}
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkText(c, sb)
	}
	if n.Type == html.ElementNode && blockTags[n.Data] {
		sb.WriteByte('\n')
	}
}

// clean splits into lines, collapses whitespace per line, and drops empty or
// boilerplate lines.
func clean(raw string) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || isBoilerplate(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isBoilerplate(line string) bool {
	l := strings.ToLower(line)
	for _, b := range boilerplate {
		if strings.Contains(l, b) {
			return true
		}
	}
	return false
}

// findTitle returns the first <title> element's text, trimmed.
func findTitle(n *html.Node) string {
	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if title != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			title = strings.TrimSpace(textContent(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return title
}

// extractRating finds the chosen strategy grade. The site renders the grade
// bolded next to a "Rating:" label; look for a bold single letter A–D whose
// surrounding text mentions "rating", then fall back to a plain-text match.
func extractRating(doc *html.Node, raw string) string {
	if r := boldRating(doc); r != "" {
		return r
	}
	if m := ratingRe.FindStringSubmatch(raw); len(m) == 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// boldRating walks <strong>/<b> nodes for a single-letter grade near a
// "rating" label.
func boldRating(doc *html.Node) string {
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "strong" || n.Data == "b") {
			t := strings.TrimSpace(textContent(n))
			if len(t) == 1 && t[0] >= 'A' && t[0] <= 'D' && nearRatingLabel(n) {
				found = strings.ToUpper(t)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// blockContainers are elements that delimit a rating's local context, so the
// label search never leaks up to <body> (whose text matches "Rating:" for any
// page that has one).
var blockContainers = map[string]bool{
	"td": true, "th": true, "p": true, "div": true, "li": true,
	"blockquote": true, "article": true, "section": true, "pre": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"dd": true, "dt": true, "figure": true,
}

// nearRatingLabel reports whether the "Rating:" label sits in the grade's own
// block container (the index-page shape) or in the block immediately before
// it (the article-page "<h2>Rating…</h2><p>…grade…</p>" shape).
func nearRatingLabel(n *html.Node) bool {
	container := n.Parent
	for container != nil && !blockContainers[container.Data] {
		container = container.Parent
	}
	if container == nil {
		return false
	}
	if ratingLabelRe.MatchString(textContent(container)) {
		return true
	}
	for s := container.PrevSibling; s != nil; s = s.PrevSibling {
		if s.Type == html.ElementNode && ratingLabelRe.MatchString(textContent(s)) {
			return true
		}
		if blockContainers[s.Data] {
			break
		}
	}
	return false
}

// textContent returns the concatenated text of a node's subtree.
func textContent(n *html.Node) string {
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
