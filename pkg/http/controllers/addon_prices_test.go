package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestAddonPricesRejectInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		admin      *repo.PlatformAdmin
		status     int
	}{
		{"unauthenticated", `{}`, nil, http.StatusUnauthorized},
		{"inactive admin", `{}`, &repo.PlatformAdmin{Id: 1}, http.StatusForbidden},
		{"missing prices", `{"audioTranscriptionPriceCents":1490}`, &repo.PlatformAdmin{Id: 1, IsActive: true}, http.StatusBadRequest},
		{"null price", `{"extraConnectionPriceCents":0,"extraAgentPriceCents":0,"extraStorageGBPriceCents":null,"audioTranscriptionPriceCents":1490}`, &repo.PlatformAdmin{Id: 1, IsActive: true}, http.StatusBadRequest},
		{"fractional cents", `{"extraConnectionPriceCents":0,"extraAgentPriceCents":0,"extraStorageGBPriceCents":0,"audioTranscriptionPriceCents":14.90}`, &repo.PlatformAdmin{Id: 1, IsActive: true}, http.StatusBadRequest},
		{"negative price", `{"extraConnectionPriceCents":-1,"extraAgentPriceCents":0,"extraStorageGBPriceCents":0,"audioTranscriptionPriceCents":1490}`, &repo.PlatformAdmin{Id: 1, IsActive: true}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := iris.New()
			app.Put("/addon-prices", func(ctx iris.Context) {
				if tc.admin != nil {
					ctx.Values().Set("authenticatedPlatformAdmin", *tc.admin)
				}
				ctx.Next()
			}, UpdateSaaSAddonPrices)
			if err := app.Build(); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/addon-prices", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			app.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}
