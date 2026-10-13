package instances

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sophus/internal/repo"
	"sophus/utils/env"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name      string
		payload   map[string]interface{}
		connected bool
		found     bool
	}{
		{name: "nested connected", payload: map[string]interface{}{"data": map[string]interface{}{"connected": true}}, connected: true, found: true},
		{name: "Evolution GO capitalized fields", payload: map[string]interface{}{"data": map[string]interface{}{"Connected": true, "LoggedIn": true, "Name": "~ Operator"}}, connected: true, found: true},
		{name: "transport connected but session logged out", payload: map[string]interface{}{"data": map[string]interface{}{"Connected": true, "LoggedIn": false}}, connected: false, found: true},
		{name: "logged in but transport disconnected", payload: map[string]interface{}{"data": map[string]interface{}{"Connected": false, "LoggedIn": true}}, connected: false, found: true},
		{name: "explicit offline overrides connected flag", payload: map[string]interface{}{"status": "offline", "Connected": true, "LoggedIn": true}, connected: false, found: true},
		{name: "open state", payload: map[string]interface{}{"state": "open"}, connected: true, found: true},
		{name: "pairing state", payload: map[string]interface{}{"state": "pairing"}, connected: false, found: true},
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

func TestDeleteUsesGlobalTokenAndInstanceID(t *testing.T) {
	instanceID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/instance/delete/"+instanceID.String() {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("apikey") != "global-token" {
			t.Errorf("apikey = %q", request.Header.Get("apikey"))
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	originalBaseURL := repo.ApiBaseURL
	originalToken := env.Backend["WPP_API_GLOBAL_TOKEN"]
	repo.ApiBaseURL = server.URL
	env.Backend["WPP_API_GLOBAL_TOKEN"] = "global-token"
	t.Cleanup(func() {
		repo.ApiBaseURL = originalBaseURL
		env.Backend["WPP_API_GLOBAL_TOKEN"] = originalToken
	})

	if err := (&InstanceEVO{InstanceID: instanceID}).Delete(); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
}

func TestDeleteAcceptsConfirmedRemoteAbsenceAfterUpstreamFailure(t *testing.T) {
	instanceID := uuid.New()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("apikey") != "global-token" {
			t.Errorf("apikey = %q", request.Header.Get("apikey"))
		}
		switch {
		case request.Method == http.MethodDelete && request.URL.Path == "/instance/delete/"+instanceID.String():
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"error":"record not found"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/instance/all":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"message":"success","data":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restoreEvolutionTestConfig(t, server.URL, "global-token")

	if err := (&InstanceEVO{InstanceID: instanceID}).Delete(); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want deletion and verification", requests)
	}
}

func TestDeleteReturnsDiagnosticWhenRemoteInstanceStillExists(t *testing.T) {
	instanceID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodDelete:
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte("{\n  \"error\": \"database unavailable\"\n}"))
		case request.Method == http.MethodGet && request.URL.Path == "/instance/all":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"data":[{"id":"` + instanceID.String() + `"}]}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restoreEvolutionTestConfig(t, server.URL, "global-token")

	err := (&InstanceEVO{InstanceID: instanceID}).Delete()
	if err == nil || !strings.Contains(err.Error(), "status 500") || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("Delete() error = %v", err)
	}
}

func restoreEvolutionTestConfig(t *testing.T, baseURL, token string) {
	t.Helper()
	originalBaseURL := repo.ApiBaseURL
	originalToken := env.Backend["WPP_API_GLOBAL_TOKEN"]
	repo.ApiBaseURL = baseURL
	env.Backend["WPP_API_GLOBAL_TOKEN"] = token
	t.Cleanup(func() {
		repo.ApiBaseURL = originalBaseURL
		env.Backend["WPP_API_GLOBAL_TOKEN"] = originalToken
	})
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
