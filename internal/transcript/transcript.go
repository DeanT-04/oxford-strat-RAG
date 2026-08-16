// Package transcript extracts TED talk transcripts (and their event) from
// talk page HTML. The transcript is embedded in the page's __NEXT_DATA__ JSON.
package transcript

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// nextDataRe matches the embedded Next.js data script.
var nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

// ldJSONRe matches JSON-LD blocks, where TED embeds the talk transcript.
var ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)

// TED extracts the transcript text and event (e.g. "TED2010") from a TED talk
// page. Both are best-effort: event is empty when the page does not expose it.
func TED(body []byte) (text, event string, err error) {
	// TED embeds the transcript in a JSON-LD block.
	for _, m := range ldJSONRe.FindAllSubmatch(body, -1) {
		if len(m) != 2 {
			continue
		}
		if text, event, ok := searchTranscript(m[1]); ok {
			return normalize(text), event, nil
		}
	}
	// Fall back to the Next.js data blob.
	if m := nextDataRe.FindSubmatch(body); len(m) == 2 {
		if text, event, ok := searchTranscript(m[1]); ok {
			return normalize(text), event, nil
		}
	}
	return "", "", errors.New("transcript: no transcript found")
}

// searchTranscript parses a JSON blob and returns its "transcript" and
// "event" strings, if present.
func searchTranscript(raw []byte) (text, event string, ok bool) {
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", "", false
	}
	text, ok = findString(data, "transcript")
	if !ok || strings.TrimSpace(text) == "" {
		return "", "", false
	}
	event, _ = findString(data, "event")
	return text, strings.TrimSpace(event), true
}

// findString recursively returns the first string value stored under key.
func findString(v any, key string) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == key {
				if s, ok := val.(string); ok {
					return s, true
				}
			}
			if s, ok := findString(val, key); ok {
				return s, ok
			}
		}
	case []any:
		for _, val := range x {
			if s, ok := findString(val, key); ok {
				return s, ok
			}
		}
	}
	return "", false
}

// normalize collapses runs of whitespace to single newline-separated
// paragraphs, so the transcript chunks cleanly.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out []string
	for _, block := range strings.Split(s, "\n") {
		block = strings.Join(strings.Fields(block), " ")
		if block != "" {
			out = append(out, block)
		}
	}
	return strings.Join(out, "\n\n")
}
