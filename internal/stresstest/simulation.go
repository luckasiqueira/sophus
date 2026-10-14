package stresstest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sophus/internal/monitor"
	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12/middleware/jwt"
)

const (
	MaxAgents            = 500
	MaxInstances         = 200
	MaxMessagesPerMinute = 600
	MaxDurationMinutes   = 240
	customersPerInstance = 25
	targetsRefresh       = 10 * time.Second
	requestTimeout       = 30 * time.Second
)

var (
	ErrDisabled          = errors.New("o teste de estresse está desabilitado (STRESS_TEST_ENABLED)")
	ErrSimulationRunning = errors.New("já existe uma simulação em execução")
)

type Config struct {
	CompanyID            int `json:"companyId"`
	Agents               int `json:"agents"`
	Instances            int `json:"instances"`
	MessagesPerMinute    int `json:"messagesPerMinute"`
	DurationMinutes      int `json:"durationMinutes"`
	AgentIntervalSeconds int `json:"agentIntervalSeconds"`
}

func (config Config) Validate() error {
	switch {
	case config.CompanyID <= 0:
		return errors.New("selecione a empresa")
	case config.Agents < 0 || config.Agents > MaxAgents:
		return fmt.Errorf("agentes devem estar entre 0 e %d", MaxAgents)
	case config.Instances < 0 || config.Instances > MaxInstances:
		return fmt.Errorf("instâncias devem estar entre 0 e %d", MaxInstances)
	case config.Agents == 0 && config.Instances == 0:
		return errors.New("informe agentes ou instâncias para simular")
	case config.Instances > 0 && (config.MessagesPerMinute < 1 || config.MessagesPerMinute > MaxMessagesPerMinute):
		return fmt.Errorf("mensagens por minuto devem estar entre 1 e %d por instância", MaxMessagesPerMinute)
	case config.DurationMinutes < 1 || config.DurationMinutes > MaxDurationMinutes:
		return fmt.Errorf("a duração deve estar entre 1 e %d minutos", MaxDurationMinutes)
	case config.Agents > 0 && (config.AgentIntervalSeconds < 2 || config.AgentIntervalSeconds > 300):
		return errors.New("o intervalo entre ações dos agentes deve estar entre 2 e 300 segundos")
	}
	return nil
}

type Status struct {
	Running         bool       `json:"running"`
	Config          Config     `json:"config"`
	StartedAt       time.Time  `json:"startedAt"`
	EndsAt          time.Time  `json:"endsAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	StopReason      string     `json:"stopReason,omitempty"`
	AgentsOnline    int64      `json:"agentsOnline"`
	InstancesOnline int64      `json:"instancesOnline"`
	Requests        int64      `json:"requests"`
	Errors          int64      `json:"errors"`
	Webhooks        int64      `json:"webhooks"`
	WebhookErrors   int64      `json:"webhookErrors"`
	LastError       string     `json:"lastError,omitempty"`
}

type simulation struct {
	status          Status
	cancel          context.CancelFunc
	done            chan struct{}
	agentsOnline    atomic.Int64
	instancesOnline atomic.Int64
	requests        atomic.Int64
	errors          atomic.Int64
	webhooks        atomic.Int64
	webhookErrors   atomic.Int64
	lastError       atomic.Value
	targets         atomic.Value // []repo.StressConversationTarget
	baseURL         string
	client          *http.Client
	streamClient    *http.Client
}

var current = struct {
	sync.Mutex
	simulation *simulation
}{}

func SimulationRunning() bool {
	current.Lock()
	defer current.Unlock()
	return current.simulation != nil && current.simulation.status.Running
}

func CurrentStatus() *Status {
	current.Lock()
	defer current.Unlock()
	if current.simulation == nil {
		return nil
	}
	return current.simulation.snapshot()
}

func (s *simulation) snapshot() *Status {
	status := s.status
	status.AgentsOnline = s.agentsOnline.Load()
	status.InstancesOnline = s.instancesOnline.Load()
	status.Requests = s.requests.Load()
	status.Errors = s.errors.Load()
	status.Webhooks = s.webhooks.Load()
	status.WebhookErrors = s.webhookErrors.Load()
	if message, ok := s.lastError.Load().(string); ok {
		status.LastError = message
	}
	return &status
}

// Start creates the simulated agents and instances, then drives them through
// this server's own HTTP interface: virtual agents keep SSE streams open and
// browse simulated conversations, instances deliver messages as webhooks.
func Start(config Config) (*Status, error) {
	if !Enabled() {
		return nil, ErrDisabled
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(env.Backend["SALT_JWT"])
	port := strings.TrimSpace(env.Backend["SERVER_PORT"])
	if secret == "" || port == "" {
		return nil, errors.New("SALT_JWT e SERVER_PORT são necessários para a simulação")
	}
	current.Lock()
	if current.simulation != nil && current.simulation.status.Running {
		current.Unlock()
		return nil, ErrSimulationRunning
	}
	// Reserve the slot while the records are created.
	placeholder := &simulation{status: Status{Running: true, Config: config, StartedAt: time.Now()}}
	current.simulation = placeholder
	current.Unlock()
	release := func() {
		current.Lock()
		if current.simulation == placeholder {
			current.simulation = nil
		}
		current.Unlock()
	}

	agents, err := repo.CreateStressAgents(config.CompanyID, config.Agents)
	if err != nil {
		release()
		return nil, fmt.Errorf("não foi possível criar os agentes simulados: %w", err)
	}
	connections, err := repo.CreateStressConnections(config.CompanyID, config.Instances)
	if err != nil {
		if cleanupErr := repo.DeleteStressAgents(config.CompanyID, agents); cleanupErr != nil {
			log.Printf("stress simulation cleanup failed: company=%d error=%v", config.CompanyID, cleanupErr)
		}
		release()
		return nil, fmt.Errorf("não foi possível criar as instâncias simuladas: %w", err)
	}
	transport := &http.Transport{MaxIdleConns: 0, MaxIdleConnsPerHost: 1024, IdleConnTimeout: 90 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	sim := &simulation{
		status:       Status{Running: true, Config: config, StartedAt: time.Now(), EndsAt: time.Now().Add(time.Duration(config.DurationMinutes) * time.Minute)},
		cancel:       cancel,
		done:         make(chan struct{}),
		baseURL:      "http://127.0.0.1:" + port,
		client:       &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: noRedirects},
		streamClient: &http.Client{Transport: transport, CheckRedirect: noRedirects},
	}
	sim.targets.Store([]repo.StressConversationTarget{})
	current.Lock()
	current.simulation = sim
	current.Unlock()

	var group sync.WaitGroup
	group.Add(1)
	go func() { defer group.Done(); sim.refreshTargets(ctx, config.CompanyID) }()
	signer := jwt.NewSigner(jwt.HS256, []byte(secret), time.Duration(config.DurationMinutes+10)*time.Minute)
	for index, agent := range agents {
		token, err := signer.Sign(struct {
			Id      int `json:"id"`
			Version int `json:"version"`
		}{Id: agent.Id, Version: agent.SessionVersion})
		if err != nil {
			sim.finish("falha ao autenticar agentes")
			return nil, err
		}
		// Ramp up over ten seconds, as people logging in, not one burst.
		delay := time.Duration(index) * 10 * time.Second / time.Duration(max(1, len(agents)))
		group.Add(1)
		go func() { defer group.Done(); sim.runAgent(ctx, agent.Id, string(token), delay) }()
	}
	for index, connection := range connections {
		delay := time.Duration(index) * 5 * time.Second / time.Duration(max(1, len(connections)))
		group.Add(1)
		go func() { defer group.Done(); sim.runInstance(ctx, connection, config.MessagesPerMinute, delay) }()
	}
	go func() {
		timer := time.NewTimer(time.Until(sim.status.EndsAt))
		defer timer.Stop()
		select {
		case <-timer.C:
			sim.finish("tempo encerrado")
		case <-ctx.Done():
		}
	}()
	go func() {
		group.Wait()
		transport.CloseIdleConnections()
		close(sim.done)
	}()
	log.Printf("stress simulation started: company=%d agents=%d instances=%d rate=%d/min duration=%dmin",
		config.CompanyID, len(agents), len(connections), config.MessagesPerMinute, config.DurationMinutes)
	return sim.snapshot(), nil
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (s *simulation) finish(reason string) {
	current.Lock()
	if s.status.Running {
		now := time.Now()
		s.status.Running = false
		s.status.FinishedAt = &now
		s.status.StopReason = reason
		log.Printf("stress simulation finished: company=%d reason=%s", s.status.Config.CompanyID, reason)
	}
	current.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// Stop ends the running simulation and waits for its workers, so a revert
// that follows does not race with requests still in flight.
func Stop(reason string) bool {
	current.Lock()
	sim := current.simulation
	current.Unlock()
	if sim == nil || !sim.status.Running || sim.done == nil {
		return false
	}
	sim.finish(reason)
	select {
	case <-sim.done:
	case <-time.After(15 * time.Second):
	}
	return true
}

func (s *simulation) fail(err error) {
	s.errors.Add(1)
	s.lastError.Store(err.Error())
}

func (s *simulation) refreshTargets(ctx context.Context, companyID int) {
	ticker := time.NewTicker(targetsRefresh)
	defer ticker.Stop()
	for {
		if targets, err := repo.StressConversationTargets(companyID); err == nil {
			s.targets.Store(targets)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *simulation) request(ctx context.Context, method, path, token string, body []byte, fragment bool) error {
	request, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if fragment {
		request.Header.Set("HX-Request", "true")
	}
	started := time.Now()
	response, err := s.client.Do(request)
	failed := err != nil || response.StatusCode >= 400
	if ctx.Err() != nil {
		if response != nil {
			response.Body.Close()
		}
		return ctx.Err()
	}
	if err == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if failed {
			err = fmt.Errorf("%s %s: HTTP %d", method, strings.SplitN(path, "?", 2)[0], response.StatusCode)
		}
	}
	monitor.Simulator.Record(time.Since(started), failed)
	s.requests.Add(1)
	if err != nil {
		s.fail(err)
	}
	return err
}

// stream holds an SSE connection open and signals every event received.
func (s *simulation) stream(ctx context.Context, path, token string, events chan<- struct{}) {
	for ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
		if err != nil {
			return
		}
		request.AddCookie(&http.Cookie{Name: "token", Value: token})
		request.Header.Set("Accept", "text/event-stream")
		response, err := s.streamClient.Do(request)
		if err == nil && response.StatusCode != http.StatusOK {
			response.Body.Close()
			err = fmt.Errorf("SSE %s: HTTP %d", strings.SplitN(path, "?", 2)[0], response.StatusCode)
		}
		if err != nil {
			if ctx.Err() == nil {
				s.fail(err)
				sleep(ctx, 5*time.Second)
			}
			continue
		}
		monitor.Simulator.SSEOpen.Add(1)
		reader := bufio.NewReader(response.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			if strings.HasPrefix(line, "event:") && !strings.Contains(line, "ready") {
				monitor.Simulator.SSEEvent()
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
		response.Body.Close()
		monitor.Simulator.SSEOpen.Add(-1)
	}
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func jitter(base time.Duration) time.Duration {
	return base/2 + time.Duration(rand.Int64N(int64(base)))
}

// runAgent mimics an attendant's browser: the conversation-list stream stays
// open, the list reloads after events, and every interval the agent opens a
// simulated conversation (accepting it when pending) with its message stream.
func (s *simulation) runAgent(ctx context.Context, agentID int, token string, delay time.Duration) {
	if !sleep(ctx, delay) {
		return
	}
	s.agentsOnline.Add(1)
	defer s.agentsOnline.Add(-1)
	_ = s.request(ctx, http.MethodGet, "/messages", token, nil, false)
	events := make(chan struct{}, 1)
	go s.stream(ctx, "/sse/conversations", token, events)
	interval := time.Duration(s.status.Config.AgentIntervalSeconds) * time.Second
	next := time.NewTimer(jitter(interval))
	defer next.Stop()
	stopConversation := func() {}
	defer func() { stopConversation() }()
	lastList := time.Time{}
	tabs := []string{"active", "pending"}
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			// The interface debounces list reloads triggered by bursts of events.
			if time.Since(lastList) < time.Second {
				continue
			}
			lastList = time.Now()
			_ = s.request(ctx, http.MethodGet, "/messages/list?tab="+tabs[rand.IntN(len(tabs))], token, nil, true)
		case <-next.C:
			next.Reset(jitter(interval))
			targets, _ := s.targets.Load().([]repo.StressConversationTarget)
			visible := make([]repo.StressConversationTarget, 0, len(targets))
			for _, target := range targets {
				if target.Pending || target.AgentID == agentID {
					visible = append(visible, target)
				}
			}
			if len(visible) == 0 {
				_ = s.request(ctx, http.MethodGet, "/messages/list?tab=pending", token, nil, true)
				continue
			}
			target := visible[rand.IntN(len(visible))]
			if target.Pending && rand.IntN(2) == 0 {
				_ = s.request(ctx, http.MethodPost, "/messages/"+target.URL+"/accept", token, nil, false)
			}
			if err := s.request(ctx, http.MethodGet, "/messages/"+target.URL, token, nil, true); err != nil {
				continue
			}
			stopConversation()
			conversationCtx, cancelConversation := context.WithCancel(ctx)
			stopConversation = cancelConversation
			go s.stream(conversationCtx, "/sse?conversation="+target.URL, token, make(chan struct{}, 1))
		}
	}
}

// runInstance delivers inbound text messages from a fixed pool of simulated
// customers through the instance webhook, exactly as Evolution GO would.
func (s *simulation) runInstance(ctx context.Context, connection repo.ConnectionEVO, perMinute int, delay time.Duration) {
	if !sleep(ctx, delay) {
		return
	}
	s.instancesOnline.Add(1)
	defer s.instancesOnline.Add(-1)
	customers := make([]string, customersPerInstance)
	for index := range customers {
		customers[index] = fmt.Sprintf("%s9%09d", repo.StressNumberPrefix, rand.IntN(1_000_000_000))
	}
	interval := time.Minute / time.Duration(perMinute)
	for {
		if !sleep(ctx, jitter(interval)) {
			return
		}
		customer := rand.IntN(len(customers))
		jid := customers[customer] + "@s.whatsapp.net"
		payload, _ := json.Marshal(map[string]any{
			"event": "Message", "instanceId": connection.InstanceID, "instanceToken": connection.ConnectionKey,
			"data": map[string]any{
				"Info": map[string]any{
					"ID": "STRESS" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:20]), "Chat": jid, "Sender": jid,
					"IsFromMe": false, "IsGroup": false, "PushName": fmt.Sprintf("Cliente simulado %02d", customer+1),
					"Timestamp": time.Now().Format(time.RFC3339Nano), "Type": "text",
				},
				"Message": map[string]any{"conversation": simulatedTexts[rand.IntN(len(simulatedTexts))]},
			},
		})
		s.webhooks.Add(1)
		monitor.Simulator.Webhook()
		if err := s.request(ctx, http.MethodPost, "/webhook/"+connection.Webhook.String(), "", payload, false); err != nil && ctx.Err() == nil {
			s.webhookErrors.Add(1)
		}
	}
}

var simulatedTexts = []string{
	"Olá, gostaria de saber o status do meu pedido.",
	"Vocês têm esse produto em estoque?",
	"Qual o horário de atendimento?",
	"Obrigado!",
	"Preciso de ajuda com a segunda via do boleto.",
	"Pode me passar o link de pagamento?",
	"Bom dia, tudo bem?",
	"Ainda não recebi a confirmação.",
	"Consigo trocar o tamanho?",
	"Perfeito, aguardo o retorno.",
}
