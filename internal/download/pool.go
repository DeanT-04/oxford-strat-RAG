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

// DownloadAll downloads each URL, returning one Result per input, in input
// order. Filenames are assigned deterministically up front (see assignNames)
// so results are stable across runs regardless of worker scheduling.
func (p *Pool) DownloadAll(ctx context.Context, urls []string, conc int) []Result {
	if conc < 1 {
		conc = 1
	}
	results := make([]Result, len(urls))
	if len(urls) == 0 {
		return results
	}
	names := assignNames(urls)

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = p.downloadOne(ctx, urls[i], names[i])
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range urls {
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

// downloadOne fetches a single URL and writes it atomically.
func (p *Pool) downloadOne(ctx context.Context, rawURL, name string) Result {
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
	if !isPDFFile(tmpName) {
		res.Status = StatusFailed
		res.Err = "content is not a PDF"
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
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := path.Base(u.Path)
	if name == "/" || name == "." || name == ".." || name == "" {
		name = "document.pdf"
	} else {
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		name = illegalChars.ReplaceAllString(name, "_")
		name = strings.Trim(name, " .")
		if name == "" {
			name = "document.pdf"
		}
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			name += ".pdf"
		}
		if len(name) > 255 {
			name = name[:240] + ".pdf"
		}
	}
	return name, nil
}

// assignNames deterministically maps URLs to unique filenames, appending a
// short content hash only on collision.
func assignNames(urls []string) []string {
	names := make([]string, len(urls))
	used := make(map[string]string, len(urls))
	for i, u := range urls {
		base, err := sanitizeName(u)
		if err != nil {
			base = "document.pdf"
		}
		name := base
		if prev, ok := used[base]; ok && prev != u {
			name = withHash(base, u)
		}
		used[name] = u
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
