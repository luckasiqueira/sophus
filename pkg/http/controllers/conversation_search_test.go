package controllers

import (
	"github.com/kataras/iris/v12"
	"net/http/httptest"
	"sophus/internal/repo"
	"testing"
)

func TestConversationSearchRejectsMissingQueryAndInvalidCursor(t *testing.T) {
	app := iris.New()
	app.Get("/messages/{url:string}/search", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3})
		ctx.Next()
	}, SearchConversation)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?q=texto&offset=-1"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", "/messages/00000000-0000-0000-0000-000000000001/search"+query, nil))
		if response.Code != iris.StatusBadRequest {
			t.Fatalf("search accepted bad input: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestConversationSearchContextRejectsInvalidTarget(t *testing.T) {
	app := iris.New()
	app.Get("/messages/{url:string}/search-context", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3})
		ctx.Next()
	}, ConversationSearchContext)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "?id=0", "?id=-1", "?id=abc", "?id=1%27%20OR%201=1"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", "/messages/00000000-0000-0000-0000-000000000001/search-context"+target, nil))
		if response.Code != iris.StatusBadRequest {
			t.Fatalf("context accepted invalid target: %d %s", response.Code, response.Body.String())
		}
	}
}
