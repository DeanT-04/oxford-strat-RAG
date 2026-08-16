// Package download streams PDFs to disk with a bounded worker pool, atomic
// writes, SHA-256 checksums, resume/skip support, and collision-safe file
// naming. The worker count is governed upstream by config (60% CPU budget).
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DeanT-04/oxford-strat-RAG/internal/fetch"
)

// Fetcher is the streaming HTTP surface the pool needs. Satisfied by
// *fetch.Client; an interface keeps the pool unit-testable with a stub.
type Fetcher interface {
	Stream(ctx context.Context, url string) (*http.Response, error)
}

// Result is the outcome of downloading one URL.
type Result struct {
	URL         string
	FinalURL    string
	LocalPath   string // filename relative to OutputDir; empty on failure
	Size        int64
	SHA256      string
	ContentType string
	Status      string
	Err         string
	FetchedAt   time.Time
}

// Status values (mirrors manifest statuses).
const (
	StatusDownloaded = "downloaded"
	StatusSkipped    = "skipped"
	StatusFailed     = "failed"
)

// Pool downloads files concurrently into outputDir.
type Pool struct {
	fetch     Fetcher
	outputDir string
	maxSize   int64
	resume    bool
}

// New constructs a Pool.
func New(f Fetcher, outputDir string, maxSize int64, resume bool) *Pool {
	return &Pool{
		fetch:     f,
		outputDir: outputDir,
		maxSize:   maxSize,
		resume:    resume,
	}
}

// Target is a single download job: a URL plus its content kind, which
// determines the file extension and the content validation applied.
type Target struct {
	URL  string
	Kind string // "pdf" (default) | "html" | "video-text"
}

// DownloadAll downloads each URL as a PDF, returning one Result per input, in
// input order. It is a convenience wrapper around DownloadTargets for the
// common PDF-only case.
func (p *Pool) DownloadAll(ctx context.Context, urls []string, conc int) []Result {
	targets := make([]Target, len(urls))
	for i, u := range urls {
		targets[i] = Target{URL: u, Kind: "pdf"}
	}
	return p.DownloadTargets(ctx, targets, conc)
}

// DownloadTargets downloads each target, returning one Result per input, in
// input order. Filenames are assigned deterministically up front (see
// assignTargetNames) so results are stable across runs regardless of worker
// scheduling.
func (p *Pool) DownloadTargets(ctx context.Context, targets []Target, conc int) []Result {
	if conc < 1 {
		conc = 1
	}
	results := make([]Result, len(targets))
	if len(targets) == 0 {
		return results
	}
	names := assignTargetNames(targets)

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = p.downloadOneKind(ctx, targets[i].URL, names[i], targets[i].Kind)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range targets {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	return results
}

// downloadOne fetches a single URL as a PDF and writes it atomically.
func (p *Pool) downloadOne(ctx context.Context, rawURL, name string) Result {
	return p.downloadOneKind(ctx, rawURL, name, "pdf")
}

// downloadOneKind fetches a single URL and writes it atomically, validating
// the content against its kind.
func (p *Pool) downloadOneKind(ctx context.Context, rawURL, name, kind string) Result {
	res := Result{URL: rawURL, FetchedAt: time.Now()}

	if _, err := url.ParseRequestURI(rawURL); err != nil {
		res.Status = StatusFailed
		res.Err = "invalid url: " + err.Error()
		return res
	}

	dest, err := safeJoin(p.outputDir, name)
	if err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}

	if p.resume {
		if fi, statErr := os.Stat(dest); statErr == nil && fi.Size() > 0 {
			res.Status = StatusSkipped
			res.LocalPath = name
			res.Size = fi.Size()
			return res
		}
	}

	resp, err := p.fetch.Stream(ctx, rawURL)
	if err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()
	if resp.Request != nil {
		res.FinalURL = resp.Request.URL.String()
	}
	res.ContentType = resp.Header.Get("Content-Type")

	if err := os.MkdirAll(p.outputDir, 0o755); err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}
	tmp, err := os.CreateTemp(p.outputDir, ".part-*")
	if err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, p.maxSize+1))
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		res.Status = StatusFailed
		res.Err = copyErr.Error()
		return res
	}
	if n > p.maxSize {
		res.Status = StatusFailed
		res.Err = fmt.Sprintf("file exceeds max size %d bytes", p.maxSize)
		return res
	}
	if err := validateContent(kind, tmpName); err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}

	if err := os.Rename(tmpName, dest); err != nil {
		res.Status = StatusFailed
		res.Err = err.Error()
		return res
	}

	res.Status = StatusDownloaded
	res.LocalPath = name
	res.Size = n
	res.SHA256 = hex.EncodeToString(h.Sum(nil))
	return res
}

// illegalChars matches characters that are unsafe in a filename on any of
// the supported platforms (Windows separators and control bytes included).
var illegalChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// sanitizeName derives a safe, human-friendly filename from a URL. It takes
// only the final path segment, URL-decodes it, strips illegal characters,
// guarantees a .pdf suffix, and caps length. Returns an error for a URL that
// yields no usable name.
func sanitizeName(rawURL string) (string, error) {
	return sanitizeNameExt(rawURL, ".pdf")
}

// sanitizeNameExt is sanitizeName for an arbitrary extension (e.g. ".html",
// ".txt"). It guarantees the given suffix and caps the total length.
func sanitizeNameExt(rawURL, ext string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := path.Base(u.Path)
	if name == "/" || name == "." || name == ".." || name == "" {
		name = "document" + ext
	} else {
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		name = illegalChars.ReplaceAllString(name, "_")
		name = strings.Trim(name, " .")
		if name == "" {
			name = "document" + ext
		}
		if !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
			name += ext
		}
		if len(name) > 255 {
			name = name[:255-len(ext)] + ext
		}
	}
	return name, nil
}

// extForKind maps a content kind to the file extension used on disk.
func extForKind(kind string) string {
	switch kind {
	case "html":
		return ".html"
	case "video-text", "txt":
		return ".txt"
	default:
		return ".pdf"
	}
}

// assignNames deterministically maps URLs to unique .pdf filenames, appending
// a short content hash only on collision.
func assignNames(urls []string) []string {
	targets := make([]Target, len(urls))
	for i, u := range urls {
		targets[i] = Target{URL: u, Kind: "pdf"}
	}
	return assignTargetNames(targets)
}

// assignTargetNames deterministically maps targets to unique filenames,
// appending a short content hash only on collision.
func assignTargetNames(targets []Target) []string {
	names := make([]string, len(targets))
	used := make(map[string]string, len(targets))
	for i, t := range targets {
		ext := extForKind(t.Kind)
		base, err := sanitizeNameExt(t.URL, ext)
		if err != nil {
			base = "document" + ext
		}
		name := base
		if prev, ok := used[base]; ok && prev != t.URL {
			name = withHash(base, t.URL)
		}
		used[name] = t.URL
		names[i] = name
	}
	return names
}

// withHash inserts a short hash of rawURL before the extension.
func withHash(base, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if len(stem) > 200 {
		stem = stem[:200]
	}
	return fmt.Sprintf("%s-%s%s", stem, hex.EncodeToString(sum[:4]), ext)
}

// safeJoin joins dir and name, refusing any name that could escape dir.
func safeJoin(dir, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("unsafe filename %q", name)
	}
	full := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes output dir: %q", name)
	}
	return full, nil
}

// isPDFFile checks the head of a file on disk for the PDF magic header.
func isPDFFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	return fetch.IsPDF(buf[:n])
}

// validateContent verifies that a downloaded file's bytes match its kind.
// PDFs must carry the PDF magic header; HTML and transcript text are accepted
// as-is because discovery already scoped them to same-host page links.
func validateContent(kind, tmpName string) error {
	switch kind {
	case "pdf":
		if !isPDFFile(tmpName) {
			return fmt.Errorf("content is not a PDF")
		}
	}
	return nil
}
