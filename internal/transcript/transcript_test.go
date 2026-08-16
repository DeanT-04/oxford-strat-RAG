package transcript

import (
	"strings"
	"testing"
)

func TestTED(t *testing.T) {
	// TED embeds the transcript in a JSON-LD block.
	page := `<html><head></head><body>
		<script type="application/ld+json">{"@type":"VideoObject",
			"transcript":"Everybody talks about happiness these days.\n\nI had somebody count the number of books.",
			"event":"TED2010"}</script>
	</body></html>`

	text, event, err := TED([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "happiness") {
		t.Fatalf("transcript = %q", text)
	}
	if event != "TED2010" {
		t.Fatalf("event = %q", event)
	}
	// Whitespace normalized into paragraphs.
	if strings.Contains(text, "\n\n\n") {
		t.Fatalf("text not normalized: %q", text)
	}
}

func TestTEDNextDataFallback(t *testing.T) {
	page := `<script id="__NEXT_DATA__" type="application/json">{"props":{"transcript":"fallback text","event":"TED2011"}}</script>`
	text, event, err := TED([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "fallback text") {
		t.Fatalf("transcript = %q", text)
	}
	if event != "TED2011" {
		t.Fatalf("event = %q", event)
	}
}

func TestTEDNoData(t *testing.T) {
	if _, _, err := TED([]byte(`<html>no data</html>`)); err == nil {
		t.Fatal("expected error for missing transcript")
	}
}

func TestTEDNoTranscript(t *testing.T) {
	page := `<script id="__NEXT_DATA__" type="application/json">{"a":1}</script>`
	if _, _, err := TED([]byte(page)); err == nil {
		t.Fatal("expected error for missing transcript")
	}
}
