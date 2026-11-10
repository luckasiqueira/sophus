package flowengine

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"sophus/internal/repo"
)

const aiAgentWithSubBlocks = `{"nodes":[
	{"id":"start","type":"start","data":{}},
	{"id":"agent","type":"aiAgent","data":{"aiAgentId":7}},
	{"id":"kb","type":"aiKnowledge","data":{"content":"Atendemos de segunda a sexta."}},
	{"id":"tools","type":"aiTools","data":{"actions":[
		{"id":"a1","type":"transfer","name":"Transferir financeiro","description":"Quando pedir boleto","assignType":"department","assignId":3},
		{"id":"a2","type":"http","name":"Agendar reunião","description":"Quando quiser agendar","method":"POST","url":"https://example.com/agenda",
			"parameters":[{"name":"data","description":"Data desejada","required":true}]}
	]}}
],"edges":[
	{"id":"e1","source":"start","target":"agent"},
	{"id":"e2","source":"agent","target":"kb","sourceHandle":"knowledge"},
	{"id":"e3","source":"agent","target":"tools","sourceHandle":"tools"}
]}`

func TestValidateFlowDataAcceptsAIAgentSubBlocks(t *testing.T) {
	if err := ValidateFlowData(json.RawMessage(aiAgentWithSubBlocks)); err != nil {
		t.Fatalf("expected knowledge and actions attached to the agent to be accepted: %v", err)
	}
	if err := ValidateActiveFlowData(json.RawMessage(aiAgentWithSubBlocks)); err != nil {
		t.Fatalf("sub-blocks reached through the agent must count as reachable: %v", err)
	}
}

// The shape the flow builder saves for the three action kinds with most
// fields (web/flowbuilder.templ, defaultAIAction and aiActionsHTML).
func TestValidateFlowDataAcceptsBuilderAIActions(t *testing.T) {
	raw := `{"nodes":[{"id":"s","type":"start","position":{"x":0,"y":0},"data":{}},
		{"id":"a","type":"aiAgent","position":{"x":300,"y":0},"data":{"safeMode":true,"aiAgentId":7}},
		{"id":"t","type":"aiTools","position":{"x":300,"y":200},"data":{"actions":[
			{"id":"act_1","type":"transfer","name":"Transferir atendimento","description":"Quando pedir uma pessoa.","assignType":"department","message":"","assignId":2},
			{"id":"act_2","type":"http","name":"Agendar reunião","description":"Quando quiser agendar.","method":"POST","url":"https://example.com/agenda",
				"headers":[{"key":"Authorization","value":"Bearer x"}],"parameters":[{"name":"dataHora","description":"Data","required":true},{"name":"email","description":"","required":true}],"timeout":15000},
			{"id":"act_3","type":"contactField","name":"Salvar CPF","description":"Quando informar o CPF.","field":"custom","fieldKey":"cpf"}]}}],
		"edges":[{"id":"e1","source":"s","target":"a","sourceHandle":""},{"id":"e2","source":"a","target":"t","sourceHandle":"tools"}],"settings":{}}`
	if err := ValidateActiveFlowData(json.RawMessage(raw)); err != nil {
		t.Fatalf("flow saved by the builder was rejected: %v", err)
	}
}

func TestValidateFlowDataRejectsMisplacedAIAgentSubBlocks(t *testing.T) {
	tests := map[string]string{
		"knowledge on tools handle": `{"nodes":[{"id":"agent","type":"aiAgent","data":{"aiAgentId":7}},{"id":"kb","type":"aiKnowledge","data":{"content":"x"}}],
			"edges":[{"id":"e","source":"agent","target":"kb","sourceHandle":"tools"}]}`,
		"sub-block after a message": `{"nodes":[{"id":"text","type":"sendText","data":{"message":"oi"}},{"id":"kb","type":"aiKnowledge","data":{"content":"x"}}],
			"edges":[{"id":"e","source":"text","target":"kb"}]}`,
		"output from sub-block": `{"nodes":[{"id":"kb","type":"aiKnowledge","data":{"content":"x"}},{"id":"end","type":"end","data":{}}],
			"edges":[{"id":"e","source":"kb","target":"end"}]}`,
		"empty knowledge": `{"nodes":[{"id":"kb","type":"aiKnowledge","data":{"content":"  "}}],"edges":[]}`,
		"no actions":      `{"nodes":[{"id":"tools","type":"aiTools","data":{"actions":[]}}],"edges":[]}`,
		"transfer without target": `{"nodes":[{"id":"tools","type":"aiTools","data":{"actions":[
			{"type":"transfer","name":"T","description":"d","assignType":"agent"}]}}],"edges":[]}`,
		"bad parameter name": `{"nodes":[{"id":"tools","type":"aiTools","data":{"actions":[
			{"type":"http","name":"H","description":"d","url":"https://example.com","parameters":[{"name":"1data"}]}]}}],"edges":[]}`,
		"unsafe header": `{"nodes":[{"id":"tools","type":"aiTools","data":{"actions":[
			{"type":"http","name":"H","description":"d","url":"https://example.com","headers":[{"key":"Host","value":"x"}]}]}}],"edges":[]}`,
		"custom field without key": `{"nodes":[{"id":"tools","type":"aiTools","data":{"actions":[
			{"type":"contactField","name":"C","description":"d","field":"custom"}]}}],"edges":[]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateFlowData(json.RawMessage(raw)); err == nil {
				t.Fatal("expected flow to be rejected")
			}
		})
	}
}

func TestAIToolNameIsProviderSafe(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	for index, name := range []string{"Transferir p/ Financeiro", "Agendar reunião às 15h", "", strings.Repeat("ação longa ", 20)} {
		got := aiToolName(index, name)
		if !pattern.MatchString(got) {
			t.Fatalf("aiToolName(%q) = %q is not a valid function name", name, got)
		}
	}
	if got := aiToolName(1, "Agendar reunião"); got != "acao_2_agendar_reuniao" {
		t.Fatalf("got %q", got)
	}
}

func TestAIActionToolsDescribeParameters(t *testing.T) {
	data, err := ParseFlowData(json.RawMessage(aiAgentWithSubBlocks))
	if err != nil {
		t.Fatal(err)
	}
	actions, err := decodeAIActions(findNode(data.Nodes, "tools").Data)
	if err != nil {
		t.Fatal(err)
	}
	tools, byName := aiActionTools(actions)
	if len(tools) != 2 || byName[tools[1].Name].Type != "http" {
		t.Fatalf("tools=%#v", tools)
	}
	schema, _ := json.Marshal(tools[1].Parameters)
	if !strings.Contains(string(schema), `"data":{"description":"Data desejada","type":"string"}`) || !strings.Contains(string(schema), `"required":["data"]`) {
		t.Fatalf("HTTP parameters missing from schema: %s", schema)
	}
	transferSchema, _ := json.Marshal(tools[0].Parameters)
	if !strings.Contains(string(transferSchema), `"resumo"`) {
		t.Fatalf("transfer must ask for a summary: %s", transferSchema)
	}
}

func TestCollectAIAgentExtensionsResolvesFlowVariablesButKeepsParameters(t *testing.T) {
	raw := `{"nodes":[
		{"id":"agent","type":"aiAgent","data":{"aiAgentId":7}},
		{"id":"kb","type":"aiKnowledge","data":{"content":"Pedido: {{pedido}}"}},
		{"id":"tools","type":"aiTools","data":{"actions":[{"id":"a","type":"http","name":"Consultar","description":"d",
			"url":"https://example.com/{{loja}}/{{data}}","headers":[{"key":"Authorization","value":"Bearer {{token}}"}],
			"parameters":[{"name":"data","description":"Data"}]}]}}
	],"edges":[
		{"id":"e2","source":"agent","target":"kb","sourceHandle":"knowledge"},
		{"id":"e3","source":"agent","target":"tools","sourceHandle":"tools"}
	]}`
	data, err := ParseFlowData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	extensions := collectAIAgentExtensions(*findNode(data.Nodes, "agent"), data, ExecutionContext{
		"pedido": "123", "loja": "centro", "token": "abc", "data": "valor do fluxo",
	})
	if extensions.KnowledgeBase != "Pedido: 123" {
		t.Fatalf("knowledge = %q", extensions.KnowledgeBase)
	}
	if len(extensions.Actions) != 1 || extensions.Actions[0].URL != "https://example.com/centro/{{data}}" || extensions.Actions[0].Headers[0].Value != "Bearer abc" {
		t.Fatalf("actions = %#v", extensions.Actions)
	}
	encoded, _ := json.Marshal(extensions)
	if parsed := parseAIAgentExtensions(encoded); len(parsed.Actions) != 1 || parsed.KnowledgeBase != "Pedido: 123" {
		t.Fatalf("round trip lost data: %#v", parsed)
	}
}

func TestAIAgentPromptMergesFlowKnowledgeAndMentionsActions(t *testing.T) {
	prompt := buildAIAgentSystemPrompt(repo.AIAgent{Name: "Luna", KnowledgeBase: "Trocas em 30 dias."},
		AIAgentExtensions{KnowledgeBase: "Frete grátis acima de R$ 200.", Actions: []AIAction{{Type: "close"}}}, "frete")
	for _, expected := range []string{"Trocas em 30 dias.", "Frete grátis acima de R$ 200.", "AÇÕES DISPONÍVEIS"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt misses %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(buildAIAgentSystemPrompt(repo.AIAgent{Name: "Luna"}, AIAgentExtensions{}, "oi"), "AÇÕES DISPONÍVEIS") {
		t.Fatal("prompt must not mention actions when none are connected")
	}
}

func TestAIToolRunnerDefersTransferUntilReplyIsSent(t *testing.T) {
	runner := &aiToolRunner{actions: map[string]AIAction{"fechar": {ID: "a", Type: "close", Name: "Encerrar"}}}
	first, err := runner.run(runner.actions["fechar"], map[string]interface{}{"resumo": "cliente satisfeito"})
	if err != nil || runner.pending == nil || runner.pending.summary != "cliente satisfeito" || !strings.Contains(first, "após sua resposta") {
		t.Fatalf("result=%q pending=%#v err=%v", first, runner.pending, err)
	}
	if _, err := runner.run(runner.actions["fechar"], nil); err == nil {
		t.Fatal("a second transfer or closing in the same answer must be refused")
	}
}
