package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestNormalizeQuickReplyInput(t *testing.T) {
	valid := quickReplyInput{Shortcut: "  /Boas-Vindas  ", Title: "  Boas-vindas  ", Content: "  Olá!\r\n\r\nComo posso ajudar?  ", Revision: 2}
	got, err := normalizeQuickReplyInput(valid, true)
	if err != nil || got.Shortcut != "boas-vindas" || got.Title != "Boas-vindas" || got.Content != "Olá!\n\nComo posso ajudar?" || got.Revision != 2 {
		t.Fatalf("normalized = %+v, error = %v", got, err)
	}
	for _, test := range []struct {
		name   string
		input  quickReplyInput
		update bool
	}{
		{"empty shortcut", quickReplyInput{Title: "Olá", Content: "Mensagem"}, false},
		{"accented shortcut", quickReplyInput{Shortcut: "olá", Title: "Olá", Content: "Mensagem"}, false},
		{"spaces", quickReplyInput{Shortcut: "duas palavras", Title: "Olá", Content: "Mensagem"}, false},
		{"double slash", quickReplyInput{Shortcut: "//ola", Title: "Olá", Content: "Mensagem"}, false},
		{"invalid first character", quickReplyInput{Shortcut: "_ola", Title: "Olá", Content: "Mensagem"}, false},
		{"too long shortcut", quickReplyInput{Shortcut: strings.Repeat("a", 41), Title: "Olá", Content: "Mensagem"}, false},
		{"empty title", quickReplyInput{Shortcut: "ola", Title: " ", Content: "Mensagem"}, false},
		{"too long title", quickReplyInput{Shortcut: "ola", Title: strings.Repeat("á", 101), Content: "Mensagem"}, false},
		{"empty content", quickReplyInput{Shortcut: "ola", Title: "Olá", Content: "\n "}, false},
		{"too long content", quickReplyInput{Shortcut: "ola", Title: "Olá", Content: strings.Repeat("á", 10001)}, false},
		{"null content", quickReplyInput{Shortcut: "ola", Title: "Olá", Content: "abc\x00def"}, false},
		{"missing revision", quickReplyInput{Shortcut: "ola", Title: "Olá", Content: "Mensagem"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeQuickReplyInput(test.input, test.update); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := normalizeQuickReplyInput(quickReplyInput{Shortcut: strings.Repeat("a", 40), Title: strings.Repeat("á", 100), Content: strings.Repeat("á", 10000)}, false); err != nil {
		t.Fatalf("valid Unicode limits rejected: %v", err)
	}
}

func TestQuickReplyMutationsRejectOrdinaryAgents(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			app := iris.New()
			auth := func(ctx iris.Context) {
				ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent, IsActive: true})
				ctx.Next()
			}
			switch method {
			case http.MethodPost:
				app.Post("/quick-replies", auth, CreateQuickReply)
			case http.MethodPut:
				app.Put("/quick-replies", auth, UpdateQuickReply)
			case http.MethodDelete:
				app.Delete("/quick-replies", auth, DeleteQuickReply)
			}
			if err := app.Build(); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(method, "/quick-replies", strings.NewReader(`{}`)))
			if response.Code != iris.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.Code)
			}
		})
	}
}
