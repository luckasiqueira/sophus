package instances

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestReconnectRecoversDisconnectedClient(t *testing.T) {
	for _, test := range []struct {
		name            string
		reconnectStatus int
		connected       bool
		connectStatus   int
		wantCalls       int
		wantError       bool
	}{
		{"disconnected existing client", 500, false, 200, 3, false},
		{"already connected despite error", 500, true, 200, 2, false},
		{"authentication failure", 401, false, 200, 1, true},
		{"connect recovery rejected", 500, false, 500, 3, true},
		{"normal reconnect", 200, false, 200, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("apikey") != "instance-key" {
					t.Errorf("wrong authentication")
				}
				switch r.URL.Path {
				case "/instance/reconnect":
					if r.Method != "POST" {
						t.Errorf("method = %s", r.Method)
					}
					w.WriteHeader(test.reconnectStatus)
					w.Write([]byte(`{"error":"Existing client is disconnected"}`))
				case "/instance/status":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"Connected": test.connected, "LoggedIn": true}})
				case "/instance/connect":
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload["webhookUrl"] != "https://app.example/webhook/id" || payload["immediate"] != true {
						t.Errorf("unexpected connect payload: %+v", payload)
					}
					w.WriteHeader(test.connectStatus)
					w.Write([]byte(`{"error":"connection unavailable"}`))
				default:
					t.Errorf("unexpected recovery operation: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			restoreEvolutionTestConfig(t, server.URL, "global-key")
			instance := &InstanceEVO{InstanceID: uuid.New(), APIToken: "instance-key", WebhookURL: "https://app.example/webhook/id"}
			err := instance.Reconnect()
			if (err != nil) != test.wantError {
				t.Fatalf("Reconnect() = %v", err)
			}
			if calls != test.wantCalls {
				t.Fatalf("calls=%d, want %d", calls, test.wantCalls)
			}
			if test.name == "connect recovery rejected" && !strings.Contains(err.Error(), "connection unavailable") {
				t.Fatalf("missing provider diagnostic: %v", err)
			}
			if err := instance.ReconnectWithStart(func() error { t.Error("blocked request changed local connection state"); return nil }); !errors.Is(err, ErrReconnectInProgress) {
				t.Fatalf("rapid repeat was not blocked: %v", err)
			}
			if calls != test.wantCalls {
				t.Fatal("repeat reached provider")
			}
		})
	}
}

func TestReconnectGateSerializesSameInstance(t *testing.T) {
	key := uuid.NewString()
	if !beginReconnect(key) {
		t.Fatal("first request blocked")
	}
	defer endReconnect(key)
	if beginReconnect(key) {
		t.Fatal("concurrent reconnect allowed")
	}
}
