package instances

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sophus/internal/repo"
	"testing"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name      string
		payload   map[string]interface{}
		connected bool
		found     bool
	}{
		{name: "nested connected", payload: map[string]interface{}{"data": map[string]interface{}{"connected": true}}, connected: true, found: true},
		{name: "open state", payload: map[string]interface{}{"state": "open"}, connected: true, found: true},
		{name: "offline status", payload: map[string]interface{}{"status": "disconnected"}, found: true},
		{name: "unknown", payload: map[string]interface{}{"message": "ok"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, found := parseStatus(test.payload)
			if found != test.found || status.Connected != test.connected {
				t.Fatalf("parseStatus() = %#v, %v", status, found)
			}
		})
	}
}

func TestGetStatusRetriesEmptyResponse(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts++
		body, _ := io.ReadAll(request.Body)
		if len(body) != 0 {
			t.Errorf("request body = %q, want empty body", body)
		}
		if request.Header.Get("apikey") != "instance-token" {
			t.Errorf("apikey = %q", request.Header.Get("apikey"))
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"connected":true,"number":"5511999999999"}}`))
	}))
	defer server.Close()

	originalBaseURL := repo.ApiBaseURL
	repo.ApiBaseURL = server.URL
	t.Cleanup(func() { repo.ApiBaseURL = originalBaseURL })

	instance := InstanceEVO{APIToken: "instance-token"}
	status, err := instance.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus(): %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !status.Connected || status.State != "connected" || status.Number != "5511999999999" {
		t.Fatalf("status = %#v", status)
	}
}
