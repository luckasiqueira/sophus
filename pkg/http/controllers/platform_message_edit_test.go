package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func TestMessageEditRejectsInvalidInputBeforeLookup(t *testing.T) {
	app := iris.New()
	app.Post("/messages/{url:uuid}/edit", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent})
		EditConversationMessage(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{invalid`, `{"messageId":"ORIGINAL","text":"Novo"}`, `{"messageId":"ORIGINAL","text":"   ","expectedText":"Original"}`, `{"messageId":"ORIGINAL","text":"` + strings.Repeat("a", 65537) + `","expectedText":"Original"}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/messages/00000000-0000-0000-0000-000000000012/edit", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid edit accepted: HTTP %d", response.Code)
		}
	}
}
