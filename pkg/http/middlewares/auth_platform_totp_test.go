package middlewares

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"sophus/utils/env"
)

func TestPlatformSessionWithoutTOTPIsRejected(t *testing.T) {
	oldSecret := env.Backend["SALT_JWT"]
	env.Backend["SALT_JWT"] = "test-platform-session-secret"
	t.Cleanup(func() { env.Backend["SALT_JWT"] = oldSecret })
	key := sha256.Sum256([]byte(env.Backend["SALT_JWT"] + ":platform"))
	token, err := jwt.NewSigner(jwt.HS256, key[:], time.Hour).Sign(map[string]any{"id": 1, "version": 0})
	if err != nil {
		t.Fatal(err)
	}
	app := iris.New()
	app.Get("/management/api/test", AuthPlatform, func(ctx iris.Context) { t.Error("pre-2FA session reached protected handler") })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/management/api/test", nil)
	request.AddCookie(&http.Cookie{Name: "platform_token", Value: string(token)})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", response.Code)
	}
}
