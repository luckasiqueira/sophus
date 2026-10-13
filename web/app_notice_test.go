package web

import (
	"context"
	"strings"
	"testing"
)

func TestAIAndFlowUseStyledPopups(t *testing.T) {
	var ai strings.Builder
	if err := AIAgentSettings().Render(context.Background(), &ai); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ai.String(), "window.showAppNotice('Resultado do teste de IA'") || strings.Contains(ai.String(), "alert(result.message)") {
		t.Fatal("AI test must display its result through the styled notice")
	}
	var flow strings.Builder
	if err := FlowBuilder().Render(context.Background(), &flow); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="removal-confirmation"`, "await window.confirmRemoval('Remover bloco?'", "await window.confirmRemoval('Remover conexão?'", "e.target.closest('.handle, button, input, textarea, select, a')", "removeButton.onclick"} {
		if !strings.Contains(flow.String(), expected) {
			t.Fatalf("flow popup missing %q", expected)
		}
	}
	if strings.Contains(flow.String(), "confirm('Remover esse node?')") {
		t.Fatal("flow deletion still uses native confirmation")
	}
}
