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
		case route.Path == "/support/return":
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthPlatform")
		case route.Path == "/login" || route.Path == "/login/2fa" || route.Path == "/dologin" || route.Path == "/setup" || route.Path == "/logout" ||
			route.Path == "/billing" || strings.HasPrefix(route.Path, "/billing/api/") ||
			route.Path == "/payments/mercado-pago/webhook" || route.Path == "/payments/abacatepay/webhook" || route.Path == "/payments/asaas/webhook" ||
			strings.HasPrefix(route.Path, "/integrations/whatsapp/") || strings.HasPrefix(route.Path, "/media-public/") || strings.HasPrefix(route.Path, "/logos/"):
			assertNoAuthMiddleware(t, route.Method, route.Path, handlerNames)
		case strings.HasPrefix(route.Path, "/webhook/"):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthWebhook")
		case isSignedFlowMediaRoute(route.Path):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthFlowMedia")
		case route.Path == "/" || route.Path == "/settings" || route.Path == "/profile" || strings.HasPrefix(route.Path, "/companies/") || strings.HasPrefix(route.Path, "/management/api/"):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthUser")
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

func TestOperationalRoutesRequireActiveSubscription(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}

	for _, route := range app.GetRoutes() {
		operational := strings.HasPrefix(route.Path, "/messages") || strings.HasPrefix(route.Path, "/flows") || route.Path == "/contacts" ||
			strings.HasPrefix(route.Path, "/instances") || route.Path == "/sse" || route.Path == "/sse/conversations"
		if !operational {
			continue
		}
		handlerNames := make([]string, 0, len(route.Handlers))
		for _, handler := range route.Handlers {
			handlerNames = append(handlerNames, context.HandlerName(handler))
		}
		assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthSubscription")
	}
}

func TestInstanceRoutesRequireCompanyAdmin(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	for _, route := range app.GetRoutes() {
		if !strings.HasPrefix(route.Path, "/instances") {
			continue
		}
		handlerNames := make([]string, 0, len(route.Handlers))
		for _, handler := range route.Handlers {
			handlerNames = append(handlerNames, context.HandlerName(handler))
		}
		assertHasMiddleware(t, route.Method, route.Path, handlerNames, "RequireCompanyAdmin")
	}
}

func TestConversationExportRoutesRequireCompanyAdmin(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, route := range app.GetRoutes() {
		if !strings.HasPrefix(route.Path, "/messages/") || !strings.Contains(route.Path, "/export") {
			continue
		}
		count++
		var names []string
		for _, handler := range route.Handlers {
			names = append(names, context.HandlerName(handler))
		}
		assertHasMiddleware(t, route.Method, route.Path, names, "RequireCompanyAdmin")
	}
	if count != 2 {
		t.Fatalf("export routes = %d, want 2", count)
	}
}

func TestMediaCleanupRoutesRemainAvailableOutsideOperationalAccess(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	for _, route := range app.GetRoutes() {
		if route.Path != "/media" && !strings.HasPrefix(route.Path, "/management/api/media") {
			continue
		}
		for _, handler := range route.Handlers {
			if strings.HasSuffix(context.HandlerName(handler), ".AuthSubscription") {
				t.Errorf("%s %s must remain available for storage cleanup", route.Method, route.Path)
			}
		}
	}
}

func TestRouterDoesNotExposeSaaSPrefixedRoutes(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	for _, route := range app.GetRoutes() {
		if route.Path == "/saas" || strings.HasPrefix(route.Path, "/saas/") {
			t.Errorf("unexpected SaaS-prefixed route: %s %s", route.Method, route.Path)
		}
	}
}

func TestBillingPageIsPublic(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	request := httptest.NewRequest("GET", "/billing", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusOK {
		t.Fatalf("GET /billing status = %d, want %d", response.Code, iris.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "Iniciar avaliação gratuita") {
		t.Fatal("billing page does not include public registration")
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
		if strings.Contains(handler, ".AuthLogin") || strings.Contains(handler, ".AuthAPI") ||
			strings.Contains(handler, ".AuthWebhook") || strings.Contains(handler, ".AuthPlatform") ||
			strings.Contains(handler, ".AuthUser") {
			t.Errorf("public route %s %s unexpectedly uses authentication; handlers: %v", method, path, handlers)
			return
		}
	}
}
