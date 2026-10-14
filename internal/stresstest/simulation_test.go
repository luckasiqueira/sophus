package stresstest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sophus/internal/monitor"
	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/google/uuid"
)

func TestEnabledRequiresExplicitFlag(t *testing.T) {
	previous, existed := env.Backend["STRESS_TEST_ENABLED"]
	defer func() {
		if existed {
			env.Backend["STRESS_TEST_ENABLED"] = previous
		} else {
			delete(env.Backend, "STRESS_TEST_ENABLED")
		}
	}()
	for value, want := range map[string]bool{"": false, "false": false, "0": false, "talvez": false, "true": true, " TRUE ": true, "1": true, "yes": true, "on": true} {
		env.Backend["STRESS_TEST_ENABLED"] = value
		if Enabled() != want {
			t.Errorf("STRESS_TEST_ENABLED=%q: enabled=%v", value, !want)
		}
	}
	delete(env.Backend, "STRESS_TEST_ENABLED")
	if Enabled() {
		t.Fatal("stress tools enabled without the variable")
	}
	if _, err := Start(Config{CompanyID: 1, Agents: 1, DurationMinutes: 1, AgentIntervalSeconds: 5}); err != ErrDisabled {
		t.Fatalf("start while disabled: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := Config{CompanyID: 1, Agents: 10, Instances: 2, MessagesPerMinute: 30, DurationMinutes: 5, AgentIntervalSeconds: 10}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"no company": func(c *Config) { c.CompanyID = 0 }, "nothing to simulate": func(c *Config) { c.Agents, c.Instances = 0, 0 },
		"too many agents": func(c *Config) { c.Agents = MaxAgents + 1 }, "too many instances": func(c *Config) { c.Instances = MaxInstances + 1 },
		"no rate": func(c *Config) { c.MessagesPerMinute = 0 }, "too long": func(c *Config) { c.DurationMinutes = MaxDurationMinutes + 1 },
		"agent interval": func(c *Config) { c.AgentIntervalSeconds = 1 },
	} {
		config := valid
		change(&config)
		if config.Validate() == nil {
			t.Errorf("%s: accepted %+v", name, config)
		}
	}
	instancesOnly := Config{CompanyID: 1, Instances: 1, MessagesPerMinute: 1, DurationMinutes: 1}
	if err := instancesOnly.Validate(); err != nil {
		t.Fatalf("instances without agents: %v", err)
	}
}

type recordedRequest struct {
	method, path, cookie, fragment string
	body                           []byte
}

func stubServer(t *testing.T) (*httptest.Server, func() []recordedRequest) {
	var mu sync.Mutex
	requests := []recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cookie, _ := r.Cookie("token")
		record := recordedRequest{method: r.Method, path: r.URL.RequestURI(), fragment: r.Header.Get("HX-Request"), body: body}
		if cookie != nil {
			record.cookie = cookie.Value
		}
		mu.Lock()
		requests = append(requests, record)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/sse") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: ready\ndata: ok\n\nevent: conversation\ndata: refresh\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), requests...)
	}
}

func testSimulation(server *httptest.Server, interval int) *simulation {
	sim := &simulation{
		status:       Status{Running: true, Config: Config{AgentIntervalSeconds: interval}},
		baseURL:      server.URL,
		client:       &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{},
	}
	sim.targets.Store([]repo.StressConversationTarget{})
	return sim
}

func TestInstanceDeliversValidEvolutionMessages(t *testing.T) {
	server, requests := stubServer(t)
	sim := testSimulation(server, 0)
	connection := repo.ConnectionEVO{Id: 6, CompanyID: 1, Number: "0001112223334", InstanceID: uuid.New(), Webhook: uuid.New(), ConnectionKey: uuid.New()}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	sim.runInstance(ctx, connection, MaxMessagesPerMinute, 0)
	posted := requests()
	if len(posted) < 3 {
		t.Fatalf("only %d webhooks delivered at %d/min", len(posted), MaxMessagesPerMinute)
	}
	ids := map[string]bool{}
	for _, request := range posted {
		if request.method != http.MethodPost || request.path != "/webhook/"+connection.Webhook.String() || request.cookie != "" {
			t.Fatalf("unexpected webhook request: %+v", request)
		}
		var event repo.EventEVO
		var message repo.EventMessageEVO
		if json.Unmarshal(request.body, &event) != nil || json.Unmarshal(request.body, &message) != nil {
			t.Fatalf("payload is not an Evolution event: %s", request.body)
		}
		if strings.ToLower(event.EventType) != "message" || event.InstanceID != connection.InstanceID || event.InstanceToken != connection.ConnectionKey {
			t.Fatalf("webhook authentication fields mismatch: %+v", event)
		}
		message.FullJSON = request.body
		repo.NormalizeWebhookMessage(&message, connection)
		info := message.Data.Info
		if info.IsFromMe || info.IsGroup || !strings.HasPrefix(info.Sender, repo.StressNumberPrefix) || info.PushName == "" || ids[info.ID] {
			t.Fatalf("message would not arrive as a new inbound customer message: %+v", info)
		}
		if repo.CheckMessageText(message) == "" || repo.CheckMessageType(message) != "text" {
			t.Fatalf("message has no text body: %s", request.body)
		}
		ids[info.ID] = true
	}
	if sim.webhooks.Load() != int64(len(posted)) || sim.webhookErrors.Load() != 0 {
		t.Fatalf("counters: webhooks=%d errors=%d requests=%d", sim.webhooks.Load(), sim.webhookErrors.Load(), len(posted))
	}
}

func TestVirtualAgentOnlyOpensSimulatedConversations(t *testing.T) {
	server, requests := stubServer(t)
	sim := testSimulation(server, 1)
	sim.targets.Store([]repo.StressConversationTarget{
		{URL: "11111111-1111-1111-1111-111111111111", Pending: true},
		{URL: "22222222-2222-2222-2222-222222222222", AgentID: 99},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()
	sim.runAgent(ctx, 7, "agent-token", 0)
	seen := map[string]int{}
	for _, request := range requests() {
		if request.cookie != "agent-token" {
			t.Fatalf("request without the agent session: %+v", request)
		}
		path := strings.SplitN(request.path, "?", 2)[0]
		if strings.Contains(path, "22222222") {
			t.Fatalf("agent opened a conversation owned by another agent: %s", request.path)
		}
		if strings.HasPrefix(path, "/messages/1111") && request.method == http.MethodGet && request.fragment != "true" {
			t.Fatalf("conversation was not requested as an htmx fragment: %+v", request)
		}
		seen[request.method+" "+path]++
	}
	for _, expected := range []string{"GET /messages", "GET /sse/conversations", "GET /messages/list", "GET /messages/11111111-1111-1111-1111-111111111111", "GET /sse"} {
		if seen[expected] == 0 {
			t.Errorf("virtual agent never requested %s; got %v", expected, seen)
		}
	}
	if sim.agentsOnline.Load() != 0 || monitor.Simulator.SSEOpen.Load() < 0 {
		t.Fatalf("agent did not go offline: online=%d", sim.agentsOnline.Load())
	}
}
