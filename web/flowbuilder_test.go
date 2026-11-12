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
		"Arraste o fundo para navegar", "Ctrl + roda do mouse para zoom", "canvas-panning", "resizeCanvasToContent", "zoom-controls", "fitView",
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

func TestFlowBuilderPhoneStepList(t *testing.T) {
	runControlDOMTest(t, FlowBuilder(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/flows/', pretendToBeVisual: true});
const w = dom.window, d = w.document;
w.matchMedia = () => ({ matches: true, addEventListener() {}, removeEventListener() {} });
w.HTMLElement.prototype.scrollIntoView = function () {};
w.fetch = async () => ({ ok: true, status: 200, json: async () => ({ data: [] }), text: async () => '{"data":[]}' });
const script = Array.from(d.querySelectorAll('script')).find(s => s.textContent.includes('function renderMobileSteps'));
w.eval(script.textContent + ';window.__flow={state,renderCanvas,renderInspector,renderPalette,showView,defaultDataFor,openMobilePalette};');
(async () => {
await new Promise(resolve => setTimeout(resolve, 50)); // let boot() finish against the stubbed API
const f = w.__flow, N = (id, type, data) => ({ id, type, position: { x: 0, y: 0 }, data: Object.assign(f.defaultDataFor(type), data) });
f.state.currentFlow = { id: 1, name: 'Teste', triggerType: 'always', isActive: true };
f.state.flowData = {
  nodes: [N('start', 'start', {}), N('hours', 'businessHours', {}), N('msg', 'sendText', { message: 'Fora do horário' }), N('loose', 'sendText', { message: 'Solto' })],
  edges: [{ id: 'e1', source: 'start', target: 'hours', sourceHandle: '' }, { id: 'e2', source: 'hours', target: 'msg', sourceHandle: 'false' }]
};
f.renderPalette(); f.showView('editor'); f.renderCanvas();
const list = d.getElementById('mobile-steps');
const order = Array.from(list.querySelectorAll('[data-mobile-node]')).map(b => b.dataset.mobileNode);
assert.deepEqual(order, ['start', 'hours', 'msg', 'loose'], 'steps follow the flow, loose blocks last');
assert.ok(list.textContent.includes('Dentro do horário') && list.textContent.includes('Fora do horário'), 'branches are labelled');
assert.ok(list.textContent.includes('Blocos sem conexão'));
const openOutput = list.querySelector('[data-mobile-add="hours"][data-mobile-handle="true"]');
assert.ok(openOutput, 'an unconnected output offers "Adicionar bloco"');
openOutput.click();
assert.ok(d.getElementById('view-editor').classList.contains('mobile-palette-open'));
d.querySelector('#palette [data-add="sendText"]').click();
const added = f.state.flowData.nodes[f.state.flowData.nodes.length - 1];
assert.ok(f.state.flowData.edges.some(e => e.source === 'hours' && e.sourceHandle === 'true' && e.target === added.id), 'new block is connected to that output');
assert.equal(d.getElementById('view-editor').classList.contains('mobile-palette-open'), false);
assert.equal(f.state.selectedNodeId, added.id, 'the new block opens for editing');
list.querySelector('[data-mobile-node="msg"]').click();
assert.equal(f.state.selectedNodeId, 'msg');
assert.ok(d.getElementById('view-editor').classList.contains('mobile-inspector-open'));
d.getElementById('mobile-sheet-backdrop').click();
assert.equal(f.state.selectedNodeId, null, 'tapping outside closes the sheet');
dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
