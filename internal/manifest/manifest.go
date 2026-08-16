// Package manifest records the outcome of a scrape run as a JSON document
// written atomically. It is the machine-readable source of truth that the
// later ingest/RAG phase will consume.
package manifest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Entry describes a single discovered resource and the result of fetching it.
// Kind distinguishes PDFs from HTML articles, links, and video transcripts.
type Entry struct {
	URL         string    `json:"url"`
	FinalURL    string    `json:"final_url,omitempty"`
	LocalPath   string    `json:"local_path,omitempty"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Status      string    `json:"status"`
	FoundOn     string    `json:"found_on,omitempty"`
	Title       string    `json:"title,omitempty"`
	Rating      string    `json:"rating,omitempty"`
	Host        string    `json:"host,omitempty"`
	Person      string    `json:"person,omitempty"`
	Speaker     string    `json:"speaker,omitempty"`
	Event       string    `json:"event,omitempty"`
	License     string    `json:"license,omitempty"`
	Error       string    `json:"error,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// Manifest is the top-level document written for each run.
type Manifest struct {
	GeneratedAt time.Time `json:"generated_at"`
	Seed        string    `json:"seed"`
	Count       int       `json:"count"`
	Entries     []Entry   `json:"entries"`
}

// Status values used by Entry.Status.
const (
	StatusDownloaded = "downloaded"
	StatusSkipped    = "skipped"
	StatusFailed     = "failed"
	// StatusReference marks a resource that is documented but not stored
	// (paywall, dead host, or no captions). Ingest skips these.
	StatusReference = "reference"
)

// Kind values used by Entry.Kind.
const (
	KindPDF       = "pdf"        // PDF document (the historical default)
	KindHTML      = "html"       // HTML article/profile page
	KindLinks     = "links"      // curated external-links directory
	KindVideo     = "video"      // video talk (metadata / reference only)
	KindVideoText = "video-text" // video transcript text
)

// KindOf returns e.Kind, defaulting to KindPDF so legacy manifests that
// predate the kind field keep working unchanged.
func (e Entry) KindOf() string {
	if e.Kind == "" {
		return KindPDF
	}
	return e.Kind
}

// New assembles a Manifest with a timestamp and the current entry count.
func New(seed string, entries []Entry) *Manifest {
	return &Manifest{
		GeneratedAt: time.Now(),
		Seed:        seed,
		Count:       len(entries),
		Entries:     entries,
	}
}

// Encode writes m to w as indented JSON.
func (m *Manifest) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// WriteFile writes m to path atomically (temp file + rename) so a reader
// never observes a half-written manifest.
func (m *Manifest) WriteFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("manifest: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("manifest: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	if err := m.Encode(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("manifest: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("manifest: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("manifest: rename: %w", err)
	}
	return nil
}

// ReadFile loads and decodes a manifest previously written by WriteFile.
func ReadFile(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: decode: %w", err)
	}
	return &m, nil
}
