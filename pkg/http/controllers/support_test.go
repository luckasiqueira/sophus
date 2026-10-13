package controllers

import (
	"github.com/kataras/iris/v12"
	"net/http"
	"net/http/httptest"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"strings"
	"testing"
)

func TestSupportEntryRequiresSuperAdmin(t *testing.T) {
	app := iris.New()
	app.Post("/management/api/companies/{id:int}/support", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAdmin})
		ctx.Next()
	}, StartCompanySupport)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("POST", "/management/api/companies/3/support", strings.NewReader(`{}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("company admin entered support: %d", response.Code)
	}
}

func TestSupportCannotModifyCustomerProfile(t *testing.T) {
	app := iris.New()
	app.Put("/profile", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: repo.RoleAdmin, IsSupport: true})
		ctx.Next()
	}, UpdateUserProfile)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("PUT", "/profile", strings.NewReader(`{"name":"Changed","password":"new-password"}`))
	request.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support changed personal credentials: %d", response.Code)
	}
}

func TestSupportAuditAttribution(t *testing.T) {
	app := iris.New()
	app.Get("/audit", func(ctx iris.Context) {
		ctx.Values().Set("platformSupportSession", repo.PlatformSupportSession{CompanyID: 3, Admin: repo.PlatformAdmin{Id: 50, Email: "support@example.test"}})
		if _, ok := middlewares.SupportSession(ctx); !ok {
			t.Fatal("missing support context")
		}
		event := companyActionAuditEvent(ctx, repo.Agent{Id: 7, CompanyId: 3, Email: "customer@example.test"}, "update", "contact", "9")
		if event.ActorType != "platform_support" || *event.ActorID != 50 || event.ActorEmail != "support@example.test" || *event.CompanyID != 3 {
			t.Fatalf("support action was attributed to customer: %+v", event)
		}
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/audit", nil))
}
