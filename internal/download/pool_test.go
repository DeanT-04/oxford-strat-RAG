package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pdfBody(s string) string { return "%PDF-1.4\n" + s + "\n%%EOF" }

func req(u string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, u, nil)
	return r
}

func okResp(u, body, ctype string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{ctype}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req(u),
	}
}

// stubFetcher returns a canned response per URL; unknown URLs error.
type stubFetcher struct {
	resp map[string]*http.Response
	err  error
}

func (s *stubFetcher) Stream(ctx context.Context, u string) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	if r, ok := s.resp[u]; ok {
		return r, nil
	}
	return nil, errors.New("http 404: " + u)
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://x.com/uploads/turtle-rules.pdf", "turtle-rules.pdf"},
		{"https://x.com/uploads/a%20b.pdf", "a b.pdf"},
		{"https://x.com/uploads/noext", "noext.pdf"},
		{"https://x.com/uploads/a:b*c.pdf", "a_b_c.pdf"},
		{"https://x.com/", "document.pdf"},
		{"https://x.com", "document.pdf"},
		{"https://x.com/uploads/UPPER.PDF", "UPPER.PDF"},
	}
	for _, tc := range cases {
		got, err := sanitizeName(tc.in)
		if err != nil {
			t.Errorf("sanitizeName(%q) err: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAssignNames(t *testing.T) {
	urls := []string{
		"https://x.com/a/turtle.pdf",
		"https://y.com/b/turtle.pdf", // same base name, different URL
	}
	names := assignNames(urls)
	if len(names) != 2 {
		t.Fatalf("len = %d", len(names))
	}
	if names[0] != "turtle.pdf" {
		t.Fatalf("names[0] = %q", names[0])
	}
	if names[1] == "turtle.pdf" {
		t.Fatalf("expected collision-resolved name, got %q", names[1])
	}
	if !strings.HasSuffix(names[1], ".pdf") {
		t.Fatalf("names[1] must keep .pdf extension: %q", names[1])
	}
}

func TestWithHash(t *testing.T) {
	got := withHash("turtle.pdf", "https://x/y")
	if got == "turtle.pdf" {
		t.Fatal("expected hash suffix")
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("extension must be preserved: %q", got)
	}
}

func TestSafeJoin(t *testing.T) {
	if _, err := safeJoin("dir", "a.pdf"); err != nil {
		t.Fatalf("valid name: %v", err)
	}
	for _, bad := range []string{"..", "a/b", "a\\b", "", "."} {
		if _, err := safeJoin("dir", bad); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

func TestDownloadOneSuccess(t *testing.T) {
	body := pdfBody("content")
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", body, "application/pdf"),
	}}
	dir := t.TempDir()
	p := New(f, dir, 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusDownloaded {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
	if res.LocalPath != "a.pdf" {
		t.Fatalf("local path = %q", res.LocalPath)
	}
	if res.Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", res.Size, len(body))
	}
	if res.SHA256 == "" {
		t.Fatal("sha must not be empty")
	}
	if res.ContentType != "application/pdf" {
		t.Fatalf("content type = %q", res.ContentType)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("file content mismatch")
	}
}

func TestDownloadOneNotPDF(t *testing.T) {
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", "<html>oops</html>", "text/html"),
	}}
	p := New(f, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "not a PDF") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}

func TestDownloadOneOversize(t *testing.T) {
	body := pdfBody(strings.Repeat("x", 1000))
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", body, "application/pdf"),
	}}
	p := New(f, t.TempDir(), 10, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "exceeds") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}

func TestDownloadOneResume(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "a.pdf")
	if err := os.WriteFile(fp, []byte(pdfBody("old")), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", pdfBody("new"), "application/pdf"),
	}}
	p := New(f, dir, 1<<20, true)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusSkipped {
		t.Fatalf("status = %s", res.Status)
	}
	data, _ := os.ReadFile(fp)
	if string(data) != pdfBody("old") {
		t.Fatal("existing file must not be overwritten")
	}
}

func TestDownloadOneInvalidURL(t *testing.T) {
	p := New(&stubFetcher{}, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "://bad", "a.pdf")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "invalid url") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}

func TestDownloadOneFetchError(t *testing.T) {
	f := &stubFetcher{err: errors.New("boom")}
	p := New(f, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "boom") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}

func TestDownloadOneMissing(t *testing.T) {
	f := &stubFetcher{} // no entries -> 404 error
	p := New(f, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed {
		t.Fatalf("status = %s", res.Status)
	}
}

func TestDownloadAllOrder(t *testing.T) {
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", pdfBody("a"), "application/pdf"),
		"https://x.com/b.pdf": okResp("https://x.com/b.pdf", pdfBody("b"), "application/pdf"),
	}}
	p := New(f, t.TempDir(), 1<<20, false)
	results := p.DownloadAll(context.Background(),
		[]string{"https://x.com/a.pdf", "https://x.com/b.pdf"}, 2)
	if len(results) != 2 {
		t.Fatalf("len = %d", len(results))
	}
	if results[0].URL != "https://x.com/a.pdf" || results[1].URL != "https://x.com/b.pdf" {
		t.Fatalf("order not preserved: %+v", results)
	}
	if results[0].Status != StatusDownloaded || results[1].Status != StatusDownloaded {
		t.Fatalf("statuses: %s, %s", results[0].Status, results[1].Status)
	}
}

func TestDownloadAllEmpty(t *testing.T) {
	p := New(&stubFetcher{}, t.TempDir(), 1<<20, false)
	if got := p.DownloadAll(context.Background(), nil, 4); len(got) != 0 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestSanitizeNameErrors(t *testing.T) {
	if _, err := sanitizeName("://bad"); err == nil {
		t.Fatal("expected error for unparseable URL")
	}
	long := "https://x.com/" + strings.Repeat("a", 300) + ".pdf"
	name, err := sanitizeName(long)
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > 255 {
		t.Fatalf("name too long: %d", len(name))
	}
}

func TestAssignNamesInvalidURL(t *testing.T) {
	names := assignNames([]string{"://bad"})
	if len(names) != 1 || names[0] != "document.pdf" {
		t.Fatalf("names = %q", names)
	}
}

func TestWithHashLongStem(t *testing.T) {
	base := strings.Repeat("a", 300) + ".pdf"
	got := withHash(base, "https://x/y")
	if !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("ext lost: %q", got)
	}
	if len(got) > 220 {
		t.Fatalf("stem not truncated: len %d", len(got))
	}
}

func TestIsPDFFileMissing(t *testing.T) {
	if isPDFFile(filepath.Join(t.TempDir(), "nope.pdf")) {
		t.Fatal("expected false for missing file")
	}
}

func TestDownloadOneBadName(t *testing.T) {
	p := New(&stubFetcher{}, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "..")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "unsafe filename") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}

func TestDownloadOneMkdirError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", pdfBody("c"), "application/pdf"),
	}}
	p := New(f, filepath.Join(blocker, "sub"), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed {
		t.Fatalf("status = %s, err = %s", res.Status, res.Err)
	}
}

func TestDownloadAllClampsConcurrency(t *testing.T) {
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": okResp("https://x.com/a.pdf", pdfBody("a"), "application/pdf"),
	}}
	p := New(f, t.TempDir(), 1<<20, false)
	results := p.DownloadAll(context.Background(), []string{"https://x.com/a.pdf"}, 0)
	if len(results) != 1 || results[0].Status != StatusDownloaded {
		t.Fatalf("results = %+v", results)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("boom read") }

func TestDownloadOneReadError(t *testing.T) {
	f := &stubFetcher{resp: map[string]*http.Response{
		"https://x.com/a.pdf": {
			StatusCode: 200,
			Body:       io.NopCloser(failReader{}),
			Request:    req("https://x.com/a.pdf"),
		},
	}}
	p := New(f, t.TempDir(), 1<<20, false)
	res := p.downloadOne(context.Background(), "https://x.com/a.pdf", "a.pdf")
	if res.Status != StatusFailed || !strings.Contains(res.Err, "boom read") {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
}
