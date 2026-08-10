package routers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sophus/internal/media"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/context"
)

func TestEveryRouteUsesExpectedAuthenticationMiddleware(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}

	for _, route := range app.GetRoutes() {
		handlerNames := make([]string, 0, len(route.Handlers))
		for _, handler := range route.Handlers {
			handlerNames = append(handlerNames, context.HandlerName(handler))
		}

		switch {
		case route.Path == "/login" || route.Path == "/dologin":
			assertNoAuthMiddleware(t, route.Method, route.Path, handlerNames)
		case strings.HasPrefix(route.Path, "/webhook/"):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthWebhook")
		case isSignedFlowMediaRoute(route.Path):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthFlowMedia")
		case strings.HasPrefix(route.Path, "/api/"):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthAPI")
		default:
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthLogin")
		}

		if strings.HasPrefix(route.Path, "/medias") && !isSignedFlowMediaRoute(route.Path) {
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthMediaCompany")
		}
	}
}

func TestSignedFlowMediaRouteTakesPrecedenceOverCookieMediaRoute(t *testing.T) {
	originalDirectory := env.Backend["MEDIA_DIRECTORY"]
	originalSecret := env.Backend["SALT_JWT"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() {
		env.Backend["MEDIA_DIRECTORY"] = originalDirectory
		env.Backend["SALT_JWT"] = originalSecret
	}()

	directory := filepath.Join(env.Backend["MEDIA_DIRECTORY"], "3", "flows")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create media directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "image.png"), []byte("image-data"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	token, err := media.Sign(3, "image.png")
	if err != nil {
		t.Fatalf("sign media: %v", err)
	}

	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	request := httptest.NewRequest("GET", "/medias/3/flows/image.png?token="+token, nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusOK || response.Body.String() != "image-data" {
		t.Fatalf("unexpected media response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func isSignedFlowMediaRoute(path string) bool {
	return strings.HasPrefix(path, "/medias/") && strings.Contains(path, "/flows/")
}

func assertHasMiddleware(t *testing.T, method, path string, handlers []string, middleware string) {
	t.Helper()
	for _, handler := range handlers {
		if strings.HasSuffix(handler, "."+middleware) {
			return
		}
	}
	t.Errorf("%s %s does not use %s; handlers: %v", method, path, middleware, handlers)
}

func assertNoAuthMiddleware(t *testing.T, method, path string, handlers []string) {
	t.Helper()
	for _, handler := range handlers {
		if strings.Contains(handler, ".AuthLogin") || strings.Contains(handler, ".AuthAPI") || strings.Contains(handler, ".AuthWebhook") {
			t.Errorf("public route %s %s unexpectedly uses authentication; handlers: %v", method, path, handlers)
			return
		}
	}
}
