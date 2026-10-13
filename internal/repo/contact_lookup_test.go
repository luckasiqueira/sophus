package repo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetWhatsAppContactName(t *testing.T) {
	originalURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = originalURL })
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/check" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("apikey") != "instance-key" {
			t.Fatalf("unexpected API key: %q", request.Header.Get("apikey"))
		}
		_, _ = response.Write([]byte(`{"data":{"users":[{"Name":"  Maria Silva ","JID":"5511999999999@s.whatsapp.net"}]}}`))
	}))
	defer server.Close()
	ApiBaseURL = server.URL

	name, err := GetWhatsAppContactName("5511999999999", "instance-key")
	if err != nil {
		t.Fatalf("lookup contact: %v", err)
	}
	if name != "Maria Silva" {
		t.Fatalf("name = %q, want Maria Silva", name)
	}
}
