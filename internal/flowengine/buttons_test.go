package flowengine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sophus/pkg/http/requests"
)

func TestButtonsPayloadAndRouting(t *testing.T) {
	data := map[string]interface{}{
		"title": "Atendimento", "description": "Olá, {{name}}! Escolha um setor", "footer": "",
		"buttons": []interface{}{
			map[string]interface{}{"text": "Vendas", "id": "sales", "_handleId": "sales-handle"},
			map[string]interface{}{"text": "Suporte", "id": "support_{{code}}", "_handleId": "support-handle"},
		},
	}
	ctx := ExecutionContext{"name": "Ana", "code": "42"}
	payload, err := buildButtonsPayload("5511999999999", data, ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"description":"Olá, Ana! Escolha um setor"`, `"type":"reply"`, `"displayText":"Suporte"`, `"id":"support_42"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("payload %s is missing %s", encoded, expected)
		}
	}
	// Evolution GO answers 400 ("footer is required") when the footer is
	// missing, so an empty footer is replaced by the default one.
	if !strings.Contains(string(encoded), `"footer":"`+defaultButtonsFooter+`"`) || !strings.Contains(string(encoded), `"title":"Atendimento"`) {
		t.Fatalf("payload %s lacks the required title/footer", encoded)
	}
	if got := buttonResponseHandle(data, "support_42", ctx); got != "button-support-handle" {
		t.Fatalf("selected button routed to %q", got)
	}
	if got := buttonResponseHandle(data, "invalid", ctx); got != "fallback" {
		t.Fatalf("unknown button routed to %q", got)
	}
}

// Mirrors the required fields checked by Evolution GO's /send/button handler.
func TestButtonsPayloadMeetsEvolutionRequiredFields(t *testing.T) {
	buttons := []interface{}{map[string]interface{}{"text": "Opção A", "id": "option_a", "_handleId": "h1"}}
	for name, data := range map[string]map[string]interface{}{
		"no title or footer": {"description": "Mensagem de teste", "buttons": buttons},
		"blank title":        {"title": "   ", "description": "Mensagem de teste", "footer": "", "buttons": buttons},
		"custom texts":       {"title": "Título de teste", "description": "Mensagem de teste", "footer": "Rodapé de teste", "buttons": buttons},
	} {
		payload, err := buildButtonsPayload("5500000000001", data, ExecutionContext{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(payload.Number) == "" || strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Description) == "" || strings.TrimSpace(payload.Footer) == "" {
			t.Fatalf("%s: Evolution GO would reject %+v", name, payload)
		}
		if name == "custom texts" && (payload.Title != "Título de teste" || payload.Footer != "Rodapé de teste") {
			t.Fatalf("custom title/footer replaced: %+v", payload)
		}
	}
}

func TestEvolutionRequestErrorKeepsReason(t *testing.T) {
	rejected := requests.Request{Response: requests.Response{StatusCode: 400, Body: []byte(`{"error":"footer is required"}`)}}
	if got := evolutionRequestError("envio de botões", rejected, errors.New("request failed with status 400")).Error(); !strings.Contains(got, "footer is required") || !strings.Contains(got, "400") {
		t.Fatalf("reason lost: %s", got)
	}
	plain := requests.Request{Response: requests.Response{StatusCode: 500, Body: []byte("upstream down")}}
	if got := evolutionRequestError("envio de mídia", plain, errors.New("x")).Error(); !strings.Contains(got, "upstream down") {
		t.Fatalf("plain body lost: %s", got)
	}
	network := errors.New("dial tcp: connection refused")
	if got := evolutionRequestError("envio de mídia", requests.Request{}, network); !errors.Is(got, network) {
		t.Fatalf("network error not wrapped: %v", got)
	}
}

func TestValidateButtonsNode(t *testing.T) {
	button := func(id string) interface{} {
		return map[string]interface{}{"id": id, "text": "Opção", "_handleId": id + "-handle"}
	}
	for _, tc := range []struct {
		name    string
		buttons []interface{}
	}{
		{"empty", nil},
		{"more than three", []interface{}{button("a"), button("b"), button("c"), button("d")}},
		{"duplicate id", []interface{}{button("a"), button("a")}},
		{"missing label", []interface{}{map[string]interface{}{"id": "a", "_handleId": "h"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateButtonsNode(map[string]interface{}{"description": "Escolha", "buttons": tc.buttons}); err == nil {
				t.Fatal("invalid buttons were accepted")
			}
		})
	}
}

func TestButtonsNodeOutputRoutes(t *testing.T) {
	data := map[string]interface{}{
		"description": "Escolha uma opção",
		"buttons":     []interface{}{map[string]interface{}{"id": "sales", "text": "Vendas", "_handleId": "sales-handle"}},
	}
	node := FlowNode{ID: "buttons", Type: NodeSendButtons, Data: data}
	if err := validateNodeConfiguration(node); err != nil {
		t.Fatal(err)
	}
	if !validSourceHandle(node, "button-sales-handle") || !validSourceHandle(node, "fallback") || validSourceHandle(node, "button-unknown") {
		t.Fatal("button outputs validation is incorrect")
	}
	edges := []FlowEdge{{Source: "buttons", SourceHandle: "button-sales-handle", Target: "sales"}, {Source: "buttons", SourceHandle: "fallback", Target: "other"}}
	engine := &Engine{}
	for response, want := range map[string]string{"sales": "sales", "unknown": "other"} {
		handle := buttonResponseHandle(data, response, ExecutionContext{})
		next, err := engine.getNextNode("buttons", string(NodeSendButtons), edges, nil, nil, handle, "")
		if err != nil || next != want {
			t.Fatalf("response %s routed to %s (error %v), want %s", response, next, err, want)
		}
	}
}
