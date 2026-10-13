package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestAgentDeletionRejectsNonAdminAndSelf(t *testing.T) {
	for _, test := range []struct {
		name, role   string
		target, want int
	}{
		{"ordinary agent", repo.RoleAgent, 9, iris.StatusForbidden},
		{"own account", repo.RoleAdmin, 7, iris.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := iris.New()
			app.Delete("/agents/{id:int}", func(ctx iris.Context) {
				ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: test.role, IsActive: true})
				ctx.Next()
			}, DeleteCompanyAgent)
			if err := app.Build(); err != nil {
				t.Fatal(err)
			}
			path := "/agents/9"
			if test.target == 7 {
				path = "/agents/7"
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path, nil))
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestOriginalAdminDeletionReturnsConflict(t *testing.T) {
	app := iris.New()
	app.Delete("/agent", func(ctx iris.Context) { writeAgentRepositoryError(ctx, repo.ErrOriginalCompanyAdmin, "falha") })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/agent", nil))
	if response.Code != iris.StatusConflict {
		t.Fatalf("status=%d, want 409: %s", response.Code, response.Body.String())
	}
}
