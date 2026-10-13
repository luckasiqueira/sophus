package flowengine

import (
	"encoding/json"
	"strings"
	"testing"
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
	if strings.Contains(string(encoded), `"footer"`) {
		t.Fatalf("empty footer must be omitted: %s", encoded)
	}
	if got := buttonResponseHandle(data, "support_42", ctx); got != "button-support-handle" {
		t.Fatalf("selected button routed to %q", got)
	}
	if got := buttonResponseHandle(data, "invalid", ctx); got != "fallback" {
		t.Fatalf("unknown button routed to %q", got)
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
