package routers

import (
	"strings"
	"testing"

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
		case strings.HasPrefix(route.Path, "/api/"):
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthAPI")
		default:
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthLogin")
		}

		if strings.HasPrefix(route.Path, "/medias") {
			assertHasMiddleware(t, route.Method, route.Path, handlerNames, "AuthMediaCompany")
		}
	}
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
