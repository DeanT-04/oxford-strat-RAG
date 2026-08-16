package htmltext

import (
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	body := []byte(`<html><head><title>NR7 Pattern (Test: Setup &amp; Exit)</title>
		<style>.x{color:red}</style></head><body>
		<nav><a href="/">Home</a></nav>
		<header>Sign up to receive research news</header>
		<script>var x = 1;</script>
		<h1>NR7 Pattern</h1>
		<p>The NR7 setup looks for the narrowest range of the last seven bars.</p>
		<blockquote>"Markets trend more often than they mean revert."</blockquote>
		<table><tr><td>Setup</td><td>Narrow range 7</td></tr>
		<tr><td>Entry</td><td>Break of the range</td></tr></table>
		<footer>CFTC Rule 4.41</footer>
	</body></html>`)

	res, err := Extract(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "NR7 Pattern (Test: Setup & Exit)" {
		t.Fatalf("title = %q", res.Title)
	}
	if !strings.Contains(res.Text, "NR7 Pattern") {
		t.Fatalf("text missing heading: %q", res.Text)
	}
	if !strings.Contains(res.Text, "narrowest range") {
		t.Fatalf("text missing body: %q", res.Text)
	}
	if !strings.Contains(res.Text, "Markets trend more often") {
		t.Fatalf("text missing blockquote: %q", res.Text)
	}
	if !strings.Contains(res.Text, "Setup") || !strings.Contains(res.Text, "Entry") {
		t.Fatalf("table cells lost: %q", res.Text)
	}
	// Boilerplate and scripts must not leak through.
	for _, bad := range []string{"CFTC", "research news", "var x = 1", "Home"} {
		if strings.Contains(res.Text, bad) {
			t.Fatalf("boilerplate %q leaked into text: %q", bad, res.Text)
		}
	}
}

func TestExtractRatingBold(t *testing.T) {
	body := []byte(`<html><body><p>Rating: A / B / <strong>C</strong> / D</p></body></html>`)
	res, err := Extract(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rating != "C" {
		t.Fatalf("rating = %q, want C", res.Rating)
	}
}

func TestExtractRatingPlain(t *testing.T) {
	body := []byte(`<html><body><p>Rating: B</p></body></html>`)
	res, err := Extract(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rating != "B" {
		t.Fatalf("rating = %q, want B", res.Rating)
	}
}

func TestExtractRatingArticleShape(t *testing.T) {
	// Live article page renders "V. Rating: …</h2>" then a <p> with the grade.
	body := []byte(`<html><body>
		<h2>V. Rating: NR7 Pattern | Trading Strategy</h2>
		<p>A/B/<span style="text-decoration: underline;"><strong>C</strong></span>/D</p>
	</body></html>`)
	res, err := Extract(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rating != "C" {
		t.Fatalf("rating = %q, want C", res.Rating)
	}
}

func TestExtractNoRating(t *testing.T) {
	body := []byte(`<html><body><p>No grade here.</p></body></html>`)
	res, err := Extract(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rating != "" {
		t.Fatalf("rating = %q, want empty", res.Rating)
	}
}

func TestExtractEmpty(t *testing.T) {
	res, err := Extract([]byte(`<html><body></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Text) != "" {
		t.Fatalf("text = %q, want empty", res.Text)
	}
}
