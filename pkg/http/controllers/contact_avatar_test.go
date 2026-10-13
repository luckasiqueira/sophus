package controllers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestContactAvatarUsesShortPrivateCacheAndFallback(t *testing.T) {
	contact := repo.Contact{Name: "João", AvatarURL: "https://pps.whatsapp.net/new.jpg"}
	app := iris.New()
	app.Get("/avatar", func(ctx iris.Context) { serveContactAvatar(ctx, contact, ctx.URLParam("fallback") == "1") })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "/avatar", nil))
	if response.Code != iris.StatusFound || response.Header().Get("Location") != contact.AvatarURL || response.Header().Get("Cache-Control") != "private, max-age=60" {
		t.Fatalf("avatar redirect = %d %v", response.Code, response.Header())
	}
	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "/avatar?fallback=1", nil))
	if response.Code != iris.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(response.Body.String(), ">J</text>") {
		t.Fatalf("avatar fallback = %d %s", response.Code, response.Body.String())
	}
}
