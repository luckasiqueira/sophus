package requests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	request := Request{URL: server.URL, Method: http.MethodGet, Timeout: 20 * time.Millisecond}
	err := request.Do()
	if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("Request.Do() error = %v, want client timeout", err)
	}
}
