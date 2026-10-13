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
		"Ctrl e arraste o fundo", "canvas-panning", "resizeCanvasToContent",
		"Testar e mapear resposta", "responseMappings", "/flows/api/test-http",
		"variable-accordion", "Variáveis definidas", "Respostas capturadas", "Inteligência artificial",
		"mediaTranscription')", "Conteúdo da mídia transcrita", "transcriptionError",
		"conversationSummary')", "Resumo da conversa", "data.saveScoreTo", "Nota NPS recebida",
		"messageId", "userReplied", "aiError", "scheduleError", "subflowError", "collectVariablesFromNodes",
		"safeModeField", "Ativar proteções adicionais", "não executa instruções presentes no arquivo", "durante toda esta sessão transferida",
		"Cliente sem resposta", "Detectar falta de resposta", "no-response-minutes", "no-response-target-type", "Sem resposta",
		`href="/settings#settings-ai-credentials"`, "/flows/api/ai-credentials", "credentialId", "Configurar chaves de IA",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("rendered builder does not contain %q", expected)
		}
	}
	if strings.Contains(html, `href="/agents" class="px-3`) {
		t.Error("AI credentials link still points to human agents")
	}
	if count := strings.Count(html, "html += safeModeField"); count != 4 {
		t.Fatalf("safe mode selector count = %d, want 4 AI flow blocks", count)
	}
}

func TestFlowBuilderUsesAppShellAndOffersStarterChoices(t *testing.T) {
	var output strings.Builder
	if err := FlowBuilder().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{`id="app-navigation"`, `id="flowbuilder-page"`, `id="new-flow-modal"`, "Começar em branco", "Começar a partir de modelo", "Boas-vindas simples", "Capturar uma solicitação", "Menu de informações", "Resposta por horário", "Encaminhar para a equipe", "cloneStarterGraph", "Modelos de mensagem"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("flow creation/layout missing %q", expected)
		}
	}
	if strings.Count(html, "<!doctype html>")+strings.Count(html, "<!DOCTYPE html>") != 1 || strings.Count(html, `id="removal-confirmation-modal"`) > 1 {
		t.Fatal("builder duplicated the document or global modal")
	}
}

func TestFlowBuilderServesButtonsBlock(t *testing.T) {
	var output strings.Builder
	if err := FlowBuilder().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"sendButtons:", "label: 'Botões'", "case 'sendButtons':", "renderReplyButtons(node)", "m.category === cat"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("flowbuilder page is missing the Botões block: %q", expected)
		}
	}
}
