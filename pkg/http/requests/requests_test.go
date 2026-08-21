package requests

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublicRedirectPolicy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err := publicRedirectPolicy(false)(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("disabled redirect error = %v", err)
	}
	if err := publicRedirectPolicy(true)(request, nil); err != nil {
		t.Fatalf("valid redirect error = %v", err)
	}
	invalid := httptest.NewRequest(http.MethodGet, "https://user:password@example.com/next", nil)
	if err := publicRedirectPolicy(true)(invalid, nil); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("credential redirect error = %v", err)
	}
	via := make([]*http.Request, 10)
	if err := publicRedirectPolicy(true)(request, via); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("redirect limit error = %v", err)
	}
}

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

func TestRequestUsesRawBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	request := Request{
		URL:         server.URL,
		Method:      http.MethodPost,
		Payload:     map[string]string{"wrong": "payload"},
		RequestBody: []byte("name=Ana+Maria"),
	}
	if err := request.Do(); err != nil {
		t.Fatalf("Request.Do(): %v", err)
	}
	if receivedBody != "name=Ana+Maria" {
		t.Fatalf("body = %q", receivedBody)
	}
}

func TestPublicRequestRejectsPrivateDestination(t *testing.T) {
	request := Request{URL: "http://127.0.0.1/status", Method: http.MethodGet, PublicOnly: true}
	err := request.Do()
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("Request.Do() error = %v", err)
	}
}

func TestRequestLimitsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("response larger than limit"))
	}))
	defer server.Close()

	request := Request{URL: server.URL, Method: http.MethodGet, MaxResponseBytes: 8}
	err := request.Do()
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("Request.Do() error = %v", err)
	}
}

func TestPublicAddressPolicy(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "::1", "fc00::1", "2001:db8::1"} {
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			t.Fatal(err)
		}
		if isPublicAddress(parsed) {
			t.Errorf("address %s considered public", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			t.Fatal(err)
		}
		if !isPublicAddress(parsed) {
			t.Errorf("address %s considered non-public", address)
		}
	}
}

func TestRequestWithoutPayloadHasEmptyBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	request := Request{URL: server.URL, Method: http.MethodGet}
	if err := request.Do(); err != nil {
		t.Fatalf("Request.Do(): %v", err)
	}
	if receivedBody != "" {
		t.Fatalf("body = %q, want empty body", receivedBody)
	}
}
