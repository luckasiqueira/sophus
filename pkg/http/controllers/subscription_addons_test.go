package controllers

import (
	"github.com/kataras/iris/v12"
	"net/http/httptest"
	"sophus/internal/repo"
	"strings"
	"testing"
)

func TestCompanyAddonRequestsRequireAdminAndCompleteQuantities(t *testing.T) {
	for _, test := range []struct {
		name, body, role string
		status           int
	}{
		{"operator cannot purchase", `{"extraConnections":1,"extraAgents":0,"extraStorageGB":0,"audioTranscription":false}`, repo.RoleAgent, 403},
		{"partial request", `{"extraStorageGB":10}`, repo.RoleAdmin, 400},
		{"negative request", `{"extraConnections":-1,"extraAgents":0,"extraStorageGB":0,"audioTranscription":false}`, repo.RoleAdmin, 400},
		{"fractional request", `{"extraConnections":1.5,"extraAgents":0,"extraStorageGB":0,"audioTranscription":false}`, repo.RoleAdmin, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := iris.New()
			app.Put("/addons", func(ctx iris.Context) {
				ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3, Role: test.role})
				ctx.Next()
			}, UpdateCompanyAddons)
			if err := app.Build(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("PUT", "/addons", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
