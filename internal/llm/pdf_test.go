package llm

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestExtractPDFText(t *testing.T) {
	path := writeTestPDF(t, "Knowledge from PDF")
	text, err := ExtractPDFText(path)
	if err != nil {
		t.Fatalf("extract PDF text: %v", err)
	}
	if !strings.Contains(text, "Knowledge from PDF") {
		t.Fatalf("unexpected extracted text: %q", text)
	}
}

func TestExtractPDFTextRejectsInvalidDocument(t *testing.T) {
	path := t.TempDir() + "/invalid.pdf"
	if err := os.WriteFile(path, []byte("not a PDF"), 0600); err != nil {
		t.Fatalf("write invalid PDF: %v", err)
	}
	if _, err := ExtractPDFText(path); err == nil {
		t.Fatal("expected invalid PDF to be rejected")
	}
}

func writeTestPDF(t *testing.T, text string) string {
	t.Helper()
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	offsets := make([]int, 6)
	writeObject := func(id int, content string) {
		offsets[id] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", id, content)
	}
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>")
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	writeObject(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	writeObject(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := document.Len()
	document.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for id := 1; id <= 5; id++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	path := t.TempDir() + "/knowledge.pdf"
	if err := os.WriteFile(path, document.Bytes(), 0600); err != nil {
		t.Fatalf("write test PDF: %v", err)
	}
	return path
}
