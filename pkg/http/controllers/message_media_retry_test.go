package controllers

import (
	"github.com/kataras/iris/v12"
	"net/http/httptest"
	"sophus/internal/repo"
	"testing"
)

func TestMediaRetryRejectsInvalidMessageID(t *testing.T) {
	app := iris.New()
	app.Post("/messages/{url:string}/media/retry", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 1, CompanyId: 1})
		ctx.Next()
	}, RetryConversationMedia)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("POST", "/messages/00000000-0000-0000-0000-000000000001/media/retry", nil))
	if response.Code != 400 {
		t.Fatalf("retry accepted missing message ID: %d", response.Code)
	}
}
