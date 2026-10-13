package instances

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
	"sophus/utils/env"
)

func TestEnsurePictureSubscriptionPreservesExistingSettings(t *testing.T) {
	oldURL, oldKey := repo.ApiBaseURL, env.Backend["WPP_API_GLOBAL_TOKEN"]
	t.Cleanup(func() { repo.ApiBaseURL = oldURL; env.Backend["WPP_API_GLOBAL_TOKEN"] = oldKey })
	env.Backend["WPP_API_GLOBAL_TOKEN"] = "global-test-key"
	var updates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if r.Header.Get("apikey") != "global-test-key" {
				t.Error("instance info must use global key")
			}
			_, _ = w.Write([]byte(`{"data":{"events":"MESSAGE,CALL,QRCODE","webhook":"https://app.test/webhook/existing","rabbitmqEnable":"enabled","websocketEnable":"disabled","natsEnable":"enabled"}}`))
			return
		}
		if r.URL.Path != "/instance/connect" {
			t.Errorf("unexpected update endpoint %s", r.URL.Path)
		}
		if r.Header.Get("apikey") != "instance-key" {
			t.Error("update must use instance key")
		}
		var payload struct {
			Subscribe []string `json:"subscribe"`
			Webhook   string   `json:"webhookUrl"`
			Rabbitmq  string   `json:"rabbitmqEnable"`
			Websocket string   `json:"websocketEnable"`
			Nats      string   `json:"natsEnable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if !slices.Contains(payload.Subscribe, "PICTURE") || !slices.Contains(payload.Subscribe, "CALL") || !slices.Contains(payload.Subscribe, "READ_RECEIPT") {
			t.Errorf("events = %v", payload.Subscribe)
		}
		if payload.Webhook != "https://app.test/webhook/existing" || payload.Rabbitmq != "enabled" || payload.Nats != "enabled" || payload.Websocket != "disabled" {
			t.Errorf("settings changed: %+v", payload)
		}
		updates++
		_, _ = w.Write([]byte(`{"message":"success"}`))
	}))
	defer server.Close()
	repo.ApiBaseURL = server.URL
	instance := InstanceEVO{InstanceID: uuid.New(), APIToken: "instance-key"}
	if err := instance.EnsurePictureSubscription(); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatalf("updates = %d, want 1", updates)
	}
}

func TestEnsurePictureSubscriptionDoesNotRewriteSubscribedInstances(t *testing.T) {
	oldURL := repo.ApiBaseURL
	t.Cleanup(func() { repo.ApiBaseURL = oldURL })
	for _, events := range []string{"MESSAGE,PICTURE,READ_RECEIPT", "ALL"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Error("already subscribed instance must not be updated")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"events": events}})
		}))
		repo.ApiBaseURL = server.URL
		instance := InstanceEVO{InstanceID: uuid.New(), APIToken: "instance-key"}
		if err := instance.EnsurePictureSubscription(); err != nil {
			t.Fatal(err)
		}
		server.Close()
	}
}
