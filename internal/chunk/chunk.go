// Package chunk splits extracted text into retrieval-ready chunks with the
// metadata needed to cite a source back to its PDF.
package chunk

import (
	"fmt"
	"regexp"
	"strings"
)

// Defaults for chunk sizing.
const (
	DefaultMaxLen = 1200
	DefaultMinLen = 80
)

// Doc identifies the source document a chunk belongs to.
type Doc struct {
	ID        string // stable unique key (the local file name)
	Source    string // display name of the file
	Title     string // paper/article/talk title, if known
	Kind      string // "pdf" | "html" | "video-text"
	SourceURL string // canonical URL of the source, for citations
	StartTime string // transcript timestamp (video-text only)
	Speaker   string // talk speaker (video-text only)
}

// Chunk is one retrievable unit of text.
type Chunk struct {
	ID        string `json:"id"`
	DocID     string `json:"doc_id"`
	Source    string `json:"source"`
	Title     string `json:"title"`
	Kind      string `json:"kind,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	StartTime string `json:"start_time,omitempty"`
	Speaker   string `json:"speaker,omitempty"`
	Order     int    `json:"order"`
	Text      string `json:"text"`
}

// sentenceSplitter matches a sentence terminator followed by whitespace.
var sentenceSplitter = regexp.MustCompile(`[.!?]\s+`)

// Split breaks raw into chunks between minLen and maxLen characters,
// preserving paragraph structure. Paragraphs longer than maxLen are further
// split by sentences.
func Split(doc Doc, raw string, maxLen, minLen int) []Chunk {
	if maxLen <= 0 {
		maxLen = DefaultMaxLen
	}
	if minLen <= 0 {
		minLen = DefaultMinLen
	}

	var chunks []Chunk
	order := 0
	for _, block := range splitBlocks(raw) {
		block = collapseWS(block)
		for _, piece := range splitLong(block, maxLen) {
			piece = strings.TrimSpace(piece)
			if len(piece) < minLen {
				continue
			}
			chunks = append(chunks, Chunk{
				ID:        fmt.Sprintf("%s:%d", doc.ID, order),
				DocID:     doc.ID,
				Source:    doc.Source,
				Title:     doc.Title,
				Kind:      doc.Kind,
				SourceURL: doc.SourceURL,
				StartTime: doc.StartTime,
				Speaker:   doc.Speaker,
				Order:     order,
				Text:      piece,
			})
			order++
		}
	}
	return chunks
}

// splitBlocks splits on blank lines; page breaks become paragraph breaks.
func splitBlocks(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\f", "\n\n")
	return strings.Split(raw, "\n\n")
}

// collapseWS collapses all runs of whitespace (including newlines) to single
// spaces, flattening each paragraph onto one logical line.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// splitLong leaves short blocks whole and greedily packs the sentences of an
// over-long block into chunks of at most maxLen characters.
func splitLong(block string, maxLen int) []string {
	if len(block) <= maxLen {
		return []string{block}
	}
	var out []string
	var cur strings.Builder
	for _, s := range sentenceSplitter.Split(block, -1) {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		switch {
		case cur.Len() == 0:
			cur.WriteString(s)
		case cur.Len()+1+len(s) <= maxLen:
			cur.WriteString(" ")
			cur.WriteString(s)
		default:
			out = append(out, cur.String())
			cur.Reset()
			cur.WriteString(s)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
