package web

import (
	"context"
	"strings"
	"testing"
)

func TestFlowBuilderIncludesRoadmapNodes(t *testing.T) {
	var output strings.Builder
	if err := FlowBuilder().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"setVariable", "updateContact", "businessHours", "randomSplit", "messageType", "smartAssign",
		"addNote", "callback", "subflow", "sendTemplate", "scheduleMessage", "templates-modal",
		"Gerar com IA",
		"data-variable-toggle", "Mensagem após transferir", "input[type=\"checkbox\"]:not(.hidden)",
		"Voltar ao menu anterior", "Voltar ao menu principal",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("rendered builder does not contain %q", expected)
		}
	}
}
