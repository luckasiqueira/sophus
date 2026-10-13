package controllers

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	parser "github.com/ledongthuc/pdf"
	"sophus/internal/repo"
)

func TestConversationExportRejectsNonAdministrators(t *testing.T) {
	for _, handler := range []iris.Handler{ExportCurrentConversation, ExportContactConversations} {
		app := iris.New()
		app.Get("/export/{url:string}", func(ctx iris.Context) {
			ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent})
			ctx.Next()
		}, handler)
		if err := app.Build(); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", "/export/"+uuid.NewString(), nil))
		if response.Code != iris.StatusForbidden {
			t.Fatalf("status = %d, want 403", response.Code)
		}
	}
}

func TestConversationExportPDFContainsCompleteHistory(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 30, 0, 0, time.UTC)
	export := repo.ConversationExport{
		Contact:     repo.Contact{Id: 10, Name: "João", Number: "5511999999999"},
		CompanyName: "Empresa São José", Timezone: "America/Sao_Paulo", AllConversations: true, GeneratedAt: now,
		Conversations: []repo.ExportedConversation{
			{ID: 101, URL: uuid.New(), Status: repo.ConversationStatusOpen, ConnectionName: "Principal", CreatedAt: now, UpdatedAt: now},
			{ID: 102, URL: uuid.New(), Status: repo.ConversationStatusClosed, ConnectionName: "Conexão removida", CreatedAt: now, UpdatedAt: now},
		},
	}
	for i := 0; i < 160; i++ {
		export.Conversations[0].Entries = append(export.Conversations[0].Entries, repo.ConversationExportEntry{Kind: "message", Text: "Mensagem de atendimento", CreatedAt: now})
	}
	export.Conversations[0].Entries[0].Text = "Primeira mensagem"
	export.Conversations[0].Entries[159].Text = "Última mensagem"
	export.Conversations[1].Entries = []repo.ConversationExportEntry{
		{Kind: "note", Text: "Anotação interna", SenderName: "Ana", CreatedAt: now},
		{Kind: "message", Text: "Legenda", IsFromMe: true, MediaType: "document", MediaPath: "documento.pdf", CreatedAt: now},
		{Kind: "message", Text: "Conteúdo excluído confidencial", IsDeleted: true, CreatedAt: now},
	}
	data, err := buildConversationExportPDF(export)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := parser.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if reader.NumPage() < 3 {
		t.Fatalf("expected multipage history, got %d pages", reader.NumPage())
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Histórico completo do contato", "João", "Empresa São José", "02/10/2026 12:30:00", "Primeira mensagem", "Última mensagem", "Conexão removida", "Anotação interna", "documento.pdf", "Mensagem excluída", "Conversa 2 de 2"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("missing %q in PDF", want)
		}
	}
	if strings.Contains(string(text), "Conteúdo excluído confidencial") {
		t.Fatal("deleted message content leaked")
	}
}

func TestConversationExportPDFHandlesEmptyConversations(t *testing.T) {
	data, err := buildConversationExportPDF(repo.ConversationExport{Timezone: "UTC", Conversations: []repo.ExportedConversation{{ID: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
}

func TestConversationPDFExportsSupplementaryEmojis(t *testing.T) {
	var emojiPNG bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 200, A: 255})
		}
	}
	if err := png.Encode(&emojiPNG, img); err != nil {
		t.Fatal(err)
	}
	loaded := make(map[string]int)
	data, err := buildConversationExportPDFWithEmojis(repo.ConversationExport{
		CompanyName: "Empresa 🚀", Contact: repo.Contact{Name: "João 👉"}, Timezone: "UTC",
		Conversations: []repo.ExportedConversation{{ConnectionName: "Conexão 🔌", AgentName: "Ana 👩", Entries: []repo.ConversationExportEntry{
			{Kind: "message", Text: "👉 Olá! Já está pronto 😀", SenderName: "Cliente 🧑", CreatedAt: time.Now()},
			{Kind: "note", Text: "Anotação 📝", CreatedAt: time.Now()},
		}}},
	}, func(key string) ([]byte, error) { loaded[key]++; return emojiPNG.Bytes(), nil })
	if err != nil {
		t.Fatalf("PDF failed with emojis: %v", err)
	}
	reader, err := parser.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Olá! Já está pronto", "João", "Anotação"} {
		if !strings.Contains(string(text), expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if !bytes.Contains(data, []byte("/Subtype /Image")) {
		t.Fatal("PDF contains no emoji artwork")
	}
	if strings.Contains(string(text), "[U+") {
		t.Fatal("emoji was replaced by a Unicode label")
	}
	for _, key := range []string{"1f449", "1f600", "1f680", "1f4dd"} {
		if loaded[key] != 1 {
			t.Errorf("emoji %s loaded %d times", key, loaded[key])
		}
	}
}

func TestConversationExportFilenameUsesLocalWhatsAppNumber(t *testing.T) {
	for _, test := range []struct {
		number string
		all    bool
		want   string
	}{
		{"5521987654321", true, "conversas-contato-21987654321.pdf"},
		{"+55 (21) 98765-4321", false, "conversa-21987654321.pdf"},
		{"551132165432", false, "conversa-1132165432.pdf"},
		{"55987654321", false, "conversa-55987654321.pdf"},
		{"", true, "conversas-contato-sem-numero.pdf"},
	} {
		if got := conversationExportFilename(test.number, test.all); got != test.want {
			t.Errorf("filename(%q, %v) = %q, want %q", test.number, test.all, got, test.want)
		}
	}
}
