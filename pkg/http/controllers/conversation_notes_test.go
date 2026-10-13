package controllers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestInternalNoteRejectsEmptyInput(t *testing.T) {
	app := iris.New()
	app.Post("/messages/{url:string}/notes", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3})
		ctx.Next()
	}, AddConversationNote)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/messages/00000000-0000-0000-0000-000000000001/notes", strings.NewReader(`{"content":"  "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusBadRequest {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
}

func TestWhatsAppSendRejectsInternalNote(t *testing.T) {
	app := iris.New()
	app.Post("/send", SendMessage)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/send", strings.NewReader("internalNote=true&message=privado"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusBadRequest {
		t.Fatalf("internal note accepted by send endpoint: %d %s", response.Code, response.Body.String())
	}
}
