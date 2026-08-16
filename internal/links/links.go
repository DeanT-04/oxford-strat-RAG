// Package links persists the curated external-links directory as
// data/links.json. It is pure curation: the links are pointers to external
// sites, never fetched and never indexed.
package links

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Item is one curated link.
type Item struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Blurb string `json:"blurb,omitempty"`
}

// Doc is the persisted links directory.
type Doc struct {
	Kind       string            `json:"kind"`
	CapturedAt time.Time         `json:"captured_at"`
	Source     string            `json:"source"`
	Groups     map[string][]Item `json:"groups"`
}

// New assembles a Doc from grouped items.
func New(source string, groups map[string][]Item) *Doc {
	if groups == nil {
		groups = map[string][]Item{}
	}
	return &Doc{
		Kind:       "links",
		CapturedAt: time.Now(),
		Source:     source,
		Groups:     groups,
	}
}

// WriteFile writes d atomically to path.
func (d *Doc) WriteFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("links: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".links-*.tmp")
	if err != nil {
		return fmt.Errorf("links: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		tmp.Close()
		return fmt.Errorf("links: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("links: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("links: rename: %w", err)
	}
	return nil
}

// ReadFile loads a Doc previously written by WriteFile.
func ReadFile(path string) (*Doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var d Doc
	if err := json.NewDecoder(f).Decode(&d); err != nil {
		return nil, fmt.Errorf("links: decode: %w", err)
	}
	return &d, nil
}

// Encode writes d to w as indented JSON.
func (d *Doc) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

// List writes name + URL per group to w, in deterministic group order. When
// group is non-empty, only that group is printed.
func (d *Doc) List(group string, w io.Writer) {
	keys := make([]string, 0, len(d.Groups))
	for k := range d.Groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if group != "" && group != k {
			continue
		}
		fmt.Fprintf(w, "%s:\n", k)
		for _, it := range d.Groups[k] {
			fmt.Fprintf(w, "  %s  %s\n", it.Name, it.URL)
		}
	}
}
