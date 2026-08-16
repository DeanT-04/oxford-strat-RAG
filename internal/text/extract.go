// Package text turns PDFs into plain text and provides the tokenizer used by
// both indexing and querying.
package text

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/ledongthuc/pdf"
)

// Extractor turns a PDF file into plain text. It is an interface so callers
// and tests can choose a concrete strategy.
type Extractor interface {
	Extract(path string) (string, error)
}

// PdfToText extracts text by shelling out to the poppler pdftotext binary.
// It produces the highest-fidelity text for academic PDFs.
type PdfToText struct {
	Bin string
}

// Extract runs `pdftotext -enc UTF-8 <path> -` and returns stdout.
func (p PdfToText) Extract(path string) (string, error) {
	cmd := exec.Command(p.Bin, "-enc", "UTF-8", path, "-")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

// GoPDF extracts text with a pure-Go parser. It is always available and used
// as the fallback when pdftotext is not installed.
type GoPDF struct{}

// Extract reads the PDF and returns the concatenated plain text of all pages.
func (GoPDF) Extract(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("pdf open: %w", err)
	}
	defer f.Close()

	reader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("pdf plain text: %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return "", fmt.Errorf("pdf read text: %w", err)
	}
	return buf.String(), nil
}

// Default returns the best available extractor: pdftotext when present,
// otherwise the pure-Go fallback.
func Default() Extractor {
	if bin, err := exec.LookPath("pdftotext"); err == nil {
		return PdfToText{Bin: bin}
	}
	return GoPDF{}
}
