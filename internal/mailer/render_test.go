package mailer

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleData() Data {
	return Data{
		Name:         "Ana Souza",
		CompanyName:  "Loja Exemplo Ltda",
		PlanName:     "Profissional",
		Amount:       "R$ 149,90",
		PaidAt:       "19/10/2026 14:32",
		NextDueDate:  "19/11/2026",
		AccessUntil:  "02/11/2026",
		LoginURL:     "https://app.example.com/login",
		ReceiptURL:   "https://example.com/comprovante",
		SupportEmail: "suporte@example.com",
	}
}

func TestRenderEveryKindAndVariant(t *testing.T) {
	for _, kind := range Kinds {
		for _, variant := range Variants {
			t.Run(fmt.Sprintf("%s/%s", kind, variant), func(t *testing.T) {
				rendered, err := Render(kind, variant, sampleData())
				if err != nil {
					t.Fatal(err)
				}
				if rendered.Subject == "" || rendered.Text == "" {
					t.Fatal("subject and text must be filled")
				}
				if !strings.Contains(rendered.HTML, "<!doctype html>") {
					t.Fatal("expected a full HTML document")
				}
				if !strings.Contains(rendered.HTML, "Ana Souza") {
					t.Fatal("expected the recipient name in the HTML")
				}
			})
		}
	}
}

func TestRenderEscapesUserValues(t *testing.T) {
	data := sampleData()
	data.Name = `<script>alert("x")</script>`
	rendered, err := Render(KindWelcome, VariantClassic, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.HTML, "<script>alert") {
		t.Fatal("HTML body must escape user-provided values")
	}
	if !strings.Contains(rendered.HTML, html.EscapeString(data.Name)) {
		t.Fatal("expected escaped name in HTML")
	}
}

func TestRenderHidesOptionalRows(t *testing.T) {
	data := sampleData()
	data.ReceiptURL = ""
	data.AccessUntil = ""
	rendered, err := Render(KindPaymentConfirmed, VariantBanner, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.HTML, "Ver comprovante") {
		t.Fatal("receipt button must be hidden without a receipt URL")
	}
	welcome, err := Render(KindWelcome, VariantDark, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(welcome.Text, "Período de teste") {
		t.Fatal("trial line must be hidden without AccessUntil")
	}
}

func TestRenderUnknownKind(t *testing.T) {
	if _, err := Render(Kind("nope"), VariantClassic, sampleData()); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

// TestWriteEmailPreviews writes every kind and variant as a standalone HTML file.
// It only runs when EMAIL_PREVIEW_DIR is set:
//
//	EMAIL_PREVIEW_DIR=/some/dir go test ./internal/mailer -run TestWriteEmailPreviews -v
func TestWriteEmailPreviews(t *testing.T) {
	dir := os.Getenv("EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set EMAIL_PREVIEW_DIR to write email previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kind := range Kinds {
		for _, variant := range Variants {
			rendered, err := Render(kind, variant, sampleData())
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("%s-%s.html", kind, variant)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(rendered.HTML), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
