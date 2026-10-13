package controllers

import (
	"github.com/kataras/iris/v12"
	"net/http"
	"net/http/httptest"
	"sophus/internal/repo"
	"testing"
)

func TestParseConversationProtocol(t *testing.T) {
	for _, value := range []string{"1002", "#1002", " #1002 ", "001002"} {
		if number, ok := parseConversationProtocol(value); !ok || number != 1002 {
			t.Fatalf("valid protocol %q rejected: %d", value, number)
		}
	}
	for _, value := range []string{"", "#", "0", "-1", "+1", "1.5", "1 002", "abc", "##10", "9223372036854775808", "1 OR 1=1"} {
		if _, ok := parseConversationProtocol(value); ok {
			t.Fatalf("invalid protocol accepted: %q", value)
		}
	}
}

func TestProtocolSearchRequiresAgentAndValidNumber(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		app := iris.New()
		app.Get("/messages/protocol", func(ctx iris.Context) {
			if authenticated {
				ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 7, CompanyId: 3})
			}
			ctx.Next()
		}, OpenConversationProtocol)
		if err := app.Build(); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/messages/protocol?protocol=invalid", nil))
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusBadRequest
		}
		if response.Code != want {
			t.Fatalf("status=%d want=%d", response.Code, want)
		}
	}
}
