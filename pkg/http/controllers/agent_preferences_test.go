package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func TestPreferencesPageIsAvailableToRegularAgent(t *testing.T) {
	app := iris.New()
	app.Get("/preferences", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent})
		AgentPreferencesPage(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/preferences", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="agent-preferences-form"`) {
		t.Fatalf("agent could not open preferences: HTTP %d", response.Code)
	}
}

func TestPreferencesRejectInvalidInputBeforeSaving(t *testing.T) {
	app := iris.New()
	app.Put("/preferences", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent})
		UpdateAgentPreferences(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"scrollAfterSendMode":"invalid"}`, `{"messageSignatureTemplate":"` + strings.Repeat("a", 161) + `"}`, `{invalid`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/preferences", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid preference accepted: HTTP %d", response.Code)
		}
	}
}

func TestOnboardingRejectsInvalidProgressBeforeSaving(t *testing.T) {
	app := iris.New()
	app.Post("/onboarding", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAgent})
		SaveAgentOnboarding(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"step":-1}`, `{"step":31}`, `{"completed":"yes"}`, `{invalid`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/onboarding", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid onboarding progress accepted: HTTP %d", response.Code)
		}
	}
}
