package controllers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
)

func TestPreviewCompanyMediaServesPDFInline(t *testing.T) {
	originalDirectory := env.Backend["MEDIA_DIRECTORY"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	t.Cleanup(func() { env.Backend["MEDIA_DIRECTORY"] = originalDirectory })

	directory := filepath.Join(env.Backend["MEDIA_DIRECTORY"], "3")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "contrato.pdf"), []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := iris.New()
	app.Use(middlewares.SecurityHeaders)
	app.Get("/media/{scope:string}/{file:string}/content", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3})
		PreviewCompanyMedia(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/media/messages/contrato.pdf/content", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusOK {
		t.Fatalf("preview status = %d, want %d", response.Code, iris.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("content type = %q, want application/pdf", got)
	}
	if got := response.Header().Get("Content-Disposition"); got != "inline; filename=contrato.pdf" {
		t.Fatalf("content disposition = %q, want inline", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("missing nosniff header: %q", got)
	}
	if got := response.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("frame policy = %q, want SAMEORIGIN", got)
	}
	if values := response.Header().Values("X-Frame-Options"); len(values) != 1 {
		t.Fatalf("frame policy has conflicting values: %q", values)
	}
}
