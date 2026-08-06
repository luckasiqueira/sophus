package flowengine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMenuPayloadUsesEvolutionFooterField(t *testing.T) {
	payload, err := json.Marshal(menuPayload{FooterText: "Sophus"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"footerText":"Sophus"`) {
		t.Fatalf("menu payload does not contain footerText: %s", payload)
	}
}

func TestEvolutionMessageID(t *testing.T) {
	body := []byte(`{"data":{"Info":{"ID":"message-id"}}}`)
	if got := evolutionMessageID(body); got != "message-id" {
		t.Fatalf("evolutionMessageID() = %q, want message-id", got)
	}
}

func TestFormatMenuAsText(t *testing.T) {
	payload := menuPayload{
		Title:       "Atendimento",
		Description: "Escolha um setor",
		FooterText:  "Sophus",
		Sections: []menuSection{{
			Title: "Setores",
			Rows: []menuRow{
				{Title: "Vendas", Description: "Novos pedidos"},
				{Title: "Suporte"},
			},
		}},
	}
	text := formatMenuAsText(payload)
	for _, expected := range []string{"*Atendimento*", "1. Vendas", "2. Suporte", "Responda com o número da opção.", "_Sophus_"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatted menu does not contain %q: %s", expected, text)
		}
	}
}

func TestMenuResponseHandle(t *testing.T) {
	data := map[string]interface{}{
		"sections": []interface{}{
			map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"rowId": "sales", "_handleId": "sales-handle"},
					map[string]interface{}{"rowId": "support", "_handleId": "support-handle"},
				},
			},
		},
	}

	if got := menuResponseHandle(data, " support ", ExecutionContext{}); got != "menu-support-handle" {
		t.Fatalf("menuResponseHandle() = %q, want %q", got, "menu-support-handle")
	}
	if got := menuResponseHandle(data, "2", ExecutionContext{}); got != "menu-support-handle" {
		t.Fatalf("menuResponseHandle() = %q, want menu-support-handle for option 2", got)
	}
	if got := menuResponseHandle(data, "unknown", ExecutionContext{}); got != "fallback" {
		t.Fatalf("menuResponseHandle() = %q, want fallback", got)
	}

	variableData := map[string]interface{}{
		"sections": []interface{}{map[string]interface{}{
			"rows": []interface{}{map[string]interface{}{"rowId": "sector_{{id}}", "_handleId": "sector-handle"}},
		}},
	}
	if got := menuResponseHandle(variableData, "sector_10", ExecutionContext{"id": 10}); got != "menu-sector-handle" {
		t.Fatalf("menuResponseHandle() = %q, want menu-sector-handle", got)
	}
}

func TestMergeContextRemovesNilValues(t *testing.T) {
	merged := mergeContext(
		ExecutionContext{"_waitingForMenuNodeId": "menu", "response": "old"},
		ExecutionContext{"_waitingForMenuNodeId": nil},
	)
	if _, exists := merged["_waitingForMenuNodeId"]; exists {
		t.Fatal("mergeContext() kept a context value marked for removal")
	}
	if merged["response"] != "old" {
		t.Fatal("mergeContext() removed an unrelated value")
	}
}

func TestMenuOutputDoesNotUseAnotherOption(t *testing.T) {
	edges := []FlowEdge{
		{Source: "menu", SourceHandle: "menu-sales", Target: "sales-node"},
		{Source: "menu", SourceHandle: "fallback", Target: "fallback-node"},
	}

	engine := &Engine{}
	next, err := engine.getNextNode("menu", string(NodeSendMenu), edges, nil, nil, "menu-support", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("getNextNode() = %q, want no route for an unconnected option", next)
	}

	next, err = engine.getNextNode("menu", string(NodeSendMenu), edges, nil, nil, "fallback", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "fallback-node" {
		t.Fatalf("getNextNode() = %q, want fallback-node", next)
	}

	legacy := []FlowEdge{{Source: "menu", Target: "legacy-node"}}
	next, err = engine.getNextNode("menu", string(NodeSendMenu), legacy, nil, nil, "fallback", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "legacy-node" {
		t.Fatalf("getNextNode() = %q, want legacy-node for a menu saved before named outputs", next)
	}
}
