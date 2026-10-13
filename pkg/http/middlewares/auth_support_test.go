package middlewares

import (
	"crypto/sha256"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"net/http"
	"net/http/httptest"
	"sophus/internal/repo"
	"sophus/utils/env"
	"testing"
	"time"
)

func TestSupportCannotFallBackToPlatformPrivileges(t *testing.T) {
	app := iris.New()
	app.Get("/settings", func(ctx iris.Context) {
		ctx.Values().Set(platformAdminContextKey, repo.PlatformAdmin{Id: 1, IsActive: true})
		if _, err := PlatformAdminIdentifier(ctx); err == nil {
			t.Error("company support context retained platform privileges")
		}
		ctx.StatusCode(http.StatusNoContent)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/settings", nil)
	request.AddCookie(&http.Cookie{Name: SupportCookieName, Value: "support-cookie"})
	app.ServeHTTP(httptest.NewRecorder(), request)
}

func TestInvalidSupportSessionRequiresParentPlatformSession(t *testing.T) {
	app := iris.New()
	app.Get("/management/api/test", AuthUser, func(ctx iris.Context) { t.Error("support entered without verified platform session") })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/management/api/test", nil)
	request.AddCookie(&http.Cookie{Name: SupportCookieName, Value: "invalid-support-cookie"})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestSupportSubscriptionAccessUsesVerifiedContext(t *testing.T) {
	app := iris.New()
	called := false
	app.Get("/messages", func(ctx iris.Context) {
		ctx.Values().Set(agentContextKey, repo.Agent{Id: 7, CompanyId: 3, IsSupport: true})
		ctx.Values().Set(supportContextKey, repo.PlatformSupportSession{CompanyID: 3})
		ctx.Next()
	}, AuthSubscription, func(ctx iris.Context) { called = true; ctx.StatusCode(http.StatusNoContent) })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/messages", nil))
	if !called {
		t.Fatal("support was blocked by customer subscription state")
	}
}

func TestSupportRequiresCompletedPlatformTOTP(t *testing.T) {
	old := env.Backend["SALT_JWT"]
	env.Backend["SALT_JWT"] = "support-test-secret"
	t.Cleanup(func() { env.Backend["SALT_JWT"] = old })
	key := sha256.Sum256([]byte(env.Backend["SALT_JWT"] + ":platform"))
	token, err := jwt.NewSigner(jwt.HS256, key[:], time.Hour).Sign(map[string]any{"id": 1, "version": 0})
	if err != nil {
		t.Fatal(err)
	}
	app := iris.New()
	app.Get("/management/api/test", AuthUser, func(ctx iris.Context) { t.Error("support bypassed 2FA") })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/management/api/test", nil)
	request.AddCookie(&http.Cookie{Name: platformCookieName, Value: string(token)})
	request.AddCookie(&http.Cookie{Name: SupportCookieName, Value: "support-cookie"})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}
