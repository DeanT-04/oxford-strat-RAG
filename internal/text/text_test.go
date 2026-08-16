package text

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// minimalPDF builds a valid single-page PDF whose content stream draws the
// given text, so the pure-Go extractor can be tested without external files.
func minimalPDF(text string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0} // object 0 is free

	offsets = append(offsets, b.Len())
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets = append(offsets, b.Len())
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets = append(offsets, b.Len())
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>\nendobj\n")

	content := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	offsets = append(offsets, b.Len())
	b.WriteString("4 0 obj\n<< /Length " + strconv.Itoa(len(content)) + " >>\nstream\n" + content + "\nendstream\nendobj\n")

	offsets = append(offsets, b.Len())
	b.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefStart := b.Len()
	b.WriteString("xref\n0 6\n")
	for i := 0; i <= 5; i++ {
		if i == 0 {
			b.WriteString("0000000000 65535 f \n")
		} else {
			b.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
		}
	}
	b.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n" + strconv.Itoa(xrefStart) + "\n%%EOF\n")
	return b.Bytes()
}

func TestGoPDFExtract(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "min.pdf")
	if err := os.WriteFile(p, minimalPDF("Hello World"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := (GoPDF{}).Extract(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Hello") {
		t.Fatalf("extracted text %q does not contain Hello", got)
	}
}

func TestPdfToTextError(t *testing.T) {
	if _, err := (PdfToText{Bin: filepath.Join(t.TempDir(), "nope")}).Extract("x.pdf"); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestDefault(t *testing.T) {
	if Default() == nil {
		t.Fatal("Default must return a non-nil extractor")
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("The Sharpe Ratio of Momentum and Value")
	want := []string{"sharpe", "ratio", "momentum", "value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeFiltersShortAndStopwords(t *testing.T) {
	got := Tokenize("a x z the in")
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestIsStopword(t *testing.T) {
	for _, w := range []string{"the", "and", "of", "is", "with"} {
		if !IsStopword(w) {
			t.Errorf("expected %q to be a stopword", w)
		}
	}
	for _, w := range []string{"momentum", "sharpe", "value", "trend"} {
		if IsStopword(w) {
			t.Errorf("did not expect %q to be a stopword", w)
		}
	}
}
