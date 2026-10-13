package instancesync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sophus/internal/repo"
	"sophus/internal/repo/instances"
)

func TestReconnectInterval(t *testing.T) {
	if reconnectInterval != 5*time.Minute {
		t.Fatalf("reconnectInterval = %s, want 5m", reconnectInterval)
	}
}

func TestAutomaticReconnectCandidatesExcludeInactiveConnections(t *testing.T) {
	connections := []repo.ConnectionEVO{
		{Id: 1, Status: repo.ConnectionStatusDisconnected},
		{Id: 2, Status: repo.ConnectionStatusConnected},
		{Id: 3, Status: repo.ConnectionStatusConnecting},
		{Id: 4, Status: repo.ConnectionStatusError},
	}

	candidates := automaticReconnectCandidates(connections)
	if len(candidates) != 1 || candidates[0].Id != 2 {
		t.Fatalf("automaticReconnectCandidates() = %#v, want connection 2", candidates)
	}
}

func TestShouldReconcileStatusPreservesConnectedIntent(t *testing.T) {
	tests := []struct {
		name    string
		current string
		remote  instances.Status
		want    bool
	}{
		{name: "connected instance temporarily offline", current: repo.ConnectionStatusConnected, remote: instances.Status{State: repo.ConnectionStatusDisconnected}},
		{name: "explicitly disconnected instance", current: repo.ConnectionStatusDisconnected, remote: instances.Status{State: repo.ConnectionStatusDisconnected}, want: true},
		{name: "recovered instance", current: repo.ConnectionStatusDisconnected, remote: instances.Status{Connected: true, State: repo.ConnectionStatusConnected}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldReconcileStatus(test.current, test.remote); got != test.want {
				t.Fatalf("shouldReconcileStatus() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestReconnectConnectionRequestsReconnectWhenRemoteIsDisconnected(t *testing.T) {
	reconnectRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("apikey") != "instance-token" {
			t.Errorf("apikey = %q", request.Header.Get("apikey"))
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/instance/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"connected":false}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/instance/reconnect":
			reconnectRequests++
			var payload any
			if err := json.NewDecoder(request.Body).Decode(&payload); err == nil {
				t.Errorf("reconnect request unexpectedly included payload: %#v", payload)
			}
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalBaseURL := repo.ApiBaseURL
	repo.ApiBaseURL = server.URL
	t.Cleanup(func() { repo.ApiBaseURL = originalBaseURL })

	reconnectConnection(repo.ConnectionEVO{
		Id:       7,
		APIToken: "instance-token",
	})

	if reconnectRequests != 1 {
		t.Fatalf("reconnect requests = %d, want 1", reconnectRequests)
	}
}
