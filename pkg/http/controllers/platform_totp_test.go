package controllers

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"sophus/internal/repo"
	"sophus/utils/env"
)

func TestPlatformSecondFactorRequiresPasswordChallenge(t *testing.T) {
	app := iris.New()
	app.Get("/login/2fa", PlatformSecondFactorPage)
	app.Post("/login/2fa", VerifyPlatformSecondFactor)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "/login/2fa", strings.NewReader("code=123456"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		expected := http.StatusUnauthorized
		if method == http.MethodGet {
			expected = http.StatusFound
		}
		if response.Code != expected {
			t.Fatalf("%s status=%d", method, response.Code)
		}
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == platformCookieName && cookie.Value != "" {
				t.Fatal("session issued without password challenge")
			}
		}
	}
}

func TestPlatformSessionMarksSecondFactorVerified(t *testing.T) {
	oldSecret := env.Backend["SALT_JWT"]
	env.Backend["SALT_JWT"] = "test-platform-session-secret"
	t.Cleanup(func() { env.Backend["SALT_JWT"] = oldSecret })
	app := iris.New()
	app.Get("/test", func(ctx iris.Context) {
		if err := setPlatformSession(ctx, repo.PlatformAdmin{Id: 3, SessionVersion: 2}); err != nil {
			t.Error(err)
		}
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://app.test/test", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.Name != platformCookieName {
		t.Fatalf("invalid session cookie %+v", cookie)
	}
	key := sha256.Sum256([]byte(env.Backend["SALT_JWT"] + ":platform"))
	verified, err := jwt.Verify(jwt.HS256, key[:], []byte(cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		ID      int  `json:"id"`
		Version int  `json:"version"`
		TOTP    bool `json:"totp"`
	}
	if err := verified.Claims(&claims); err != nil || !claims.TOTP || claims.ID != 3 || claims.Version != 2 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
}

func TestPlatformProfileCannotChangePassword(t *testing.T) {
	app := iris.New()
	app.Put("/profile", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedPlatformAdmin", repo.PlatformAdmin{Id: 1, IsActive: true, Email: "admin@example.com"})
		ctx.Next()
	}, UpdateSaaSProfile)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/profile", strings.NewReader(`{"name":"Admin","password":"new-password","currentPassword":"old-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("password change status=%d", response.Code)
	}
}
