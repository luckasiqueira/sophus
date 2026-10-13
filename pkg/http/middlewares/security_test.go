package middlewares

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kataras/iris/v12"
)

func TestRequireSameOrigin(t *testing.T) {
	app := iris.New()
	app.Post("/mutate", RequireSameOrigin, func(ctx iris.Context) { ctx.StatusCode(iris.StatusNoContent) })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, origin, fetchSite string
		want                    int
	}{
		{name: "same origin", origin: "https://app.example.com", fetchSite: "same-origin", want: iris.StatusNoContent},
		{name: "cross origin", origin: "https://evil.example", fetchSite: "cross-site", want: iris.StatusForbidden},
		{name: "sibling site", origin: "https://evil.example.com", fetchSite: "same-site", want: iris.StatusForbidden},
		{name: "non browser client", want: iris.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://app.example.com/mutate", nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestRequireStructuredContentType(t *testing.T) {
	app := iris.New()
	app.Post("/management/api/test", RequireStructuredContentType, func(ctx iris.Context) { ctx.StatusCode(iris.StatusNoContent) })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		contentType string
		want        int
	}{
		{contentType: "application/json; charset=utf-8", want: iris.StatusNoContent},
		{contentType: "text/plain", want: iris.StatusUnsupportedMediaType},
	} {
		request := httptest.NewRequest(http.MethodPost, "/management/api/test", strings.NewReader(`{"ok":true}`))
		request.Header.Set("Content-Type", test.contentType)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("content type %q status = %d, want %d", test.contentType, response.Code, test.want)
		}
	}
}

func TestSecurityHeadersAndBodyLimit(t *testing.T) {
	app := iris.New()
	app.Use(SecurityHeaders)
	app.Post("/limited", LimitRequestBody(4), func(ctx iris.Context) {
		_, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			ctx.StopWithStatus(iris.StatusRequestEntityTooLarge)
			return
		}
		ctx.StatusCode(iris.StatusNoContent)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.com/limited", strings.NewReader("12345"))
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", response.Code)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), microphone=(self), geolocation=()",
	} {
		if got := response.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("missing HSTS header")
	}
}

func TestIPRateLimit(t *testing.T) {
	app := iris.New()
	app.Post("/login", IPRateLimit(2, time.Minute), func(ctx iris.Context) { ctx.StatusCode(iris.StatusNoContent) })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	for attempt, want := range []int{iris.StatusNoContent, iris.StatusNoContent, iris.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, "/login", nil)
		request.RemoteAddr = "192.0.2.10:1234"
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt+1, response.Code, want)
		}
		if want == iris.StatusTooManyRequests && response.Header().Get("Retry-After") == "" {
			t.Fatal("rate limit response missing Retry-After")
		}
	}
}
