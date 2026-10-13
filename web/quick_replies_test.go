package web

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestQuickRepliesLibraryPermissionsAndComposerVisibility(t *testing.T) {
	for _, manage := range []bool{false, true} {
		var html strings.Builder
		if err := QuickReplies(manage).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html.String(), `<form id="quick-reply-form"`) != manage {
			t.Fatalf("editor visibility for manage=%v", manage)
		}
	}
	agentID := 7
	for _, status := range []string{repo.ConversationStatusOpen, repo.ConversationStatusClosed, repo.ConversationStatusRunning} {
		var html strings.Builder
		conversation := repo.Conversation{URL: uuid.New(), Status: status, AgentID: &agentID}
		if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html.String(), `id="quick-reply-tray"`) != (status == repo.ConversationStatusOpen) {
			t.Fatalf("tray visibility for status=%s", status)
		}
	}
}

func TestQuickReplyTrayInteractions(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID}
	runControlDOMTest(t, MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, nil, nil, repo.ConversationScopeCounts{}), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), { runScripts: 'outside-only', url: 'https://zubly.test/messages/test' });
const w = dom.window, d = w.document;
w.HTMLElement.prototype.scrollIntoView = function () {};
const replies = [{ id: 1, shortcut: 'boas-vindas', title: 'Boas-vindas', content: 'Olá!\nComo posso ajudar?' }, { id: 2, shortcut: 'horarios', title: 'Horários', content: '<img src=x onerror=alert(1)>\nSegunda a sexta' }];
let finish, fetches = 0;
w.fetch = () => { fetches++; return new Promise(resolve => finish = () => resolve({ok: true, json: async () => ({data: replies})})); };
for (const script of d.querySelectorAll('script')) if (script.textContent.includes("const tray = document.getElementById('quick-reply-tray')")) w.eval(script.textContent);
const composer = d.getElementById('message-composer'), input = composer.querySelector('[name=message]'), tray = d.getElementById('quick-reply-tray');
let sent = 0;
composer.addEventListener('submit', event => { event.preventDefault(); sent++; });
function type(value, caret = value.length) { input.value = value; input.setSelectionRange(caret, caret); input.dispatchEvent(new w.Event('input', {bubbles: true})); }
function key(value, extra = {}) { input.dispatchEvent(new w.KeyboardEvent('keydown', {key: value, bubbles: true, cancelable: true, ...extra})); }
(async () => {
 type('/'); assert.equal(tray.classList.contains('hidden'), false); assert.equal(fetches, 1);
 key('Enter'); assert.equal(sent, 0, 'Enter during loading must not send slash');
 type('/bo'); assert.equal(fetches, 1, 'loading must be shared');
 finish(); await new Promise(resolve => setTimeout(resolve, 0));
 assert.equal(d.querySelectorAll('#quick-reply-tray-list [role=option]').length, 1);
 assert.equal(d.getElementById('quick-reply-tray-content').textContent, replies[0].content);
 assert.equal(input.value, '/bo', 'preview must not overwrite draft');
 key('Enter'); assert.equal(input.value, replies[0].content); assert.equal(sent, 0, 'selection inserts without sending');
 assert.equal(tray.classList.contains('hidden'), true);
 assert.equal(new w.FormData(composer).get('message'), replies[0].content, 'multiline text must survive submission');
 key('Enter'); assert.equal(sent, 1, 'subsequent Enter uses normal send flow');
 type('/'); key('ArrowDown');
 assert.equal(d.getElementById('quick-reply-tray-content').textContent, replies[1].content);
 assert.equal(d.getElementById('quick-reply-tray-content').children.length, 0, 'preview is text, not executable HTML');
 key('Escape'); assert.equal(tray.classList.contains('hidden'), true); assert.equal(input.value, '/');
 type('https://example.com/path'); assert.equal(tray.classList.contains('hidden'), true, 'URL slashes are not shortcuts');
 type('Texto /boas-vindas depois', 9); d.getElementById('quick-reply-tray-insert').click();
 assert.equal(input.value, 'Texto ' + replies[0].content + ' depois', 'replace only shortcut token at caret');
 type('/hor'); d.querySelector('#message-composer button[type=submit]').click();
 assert.equal(sent, 1, 'send button during preview inserts without sending shortcut');
 assert.equal(input.value, replies[1].content);
 type('/inexistente'); assert.equal(d.getElementById('quick-reply-tray-insert').disabled, true); key('Enter'); assert.equal(sent, 1);
 d.getElementById('quick-reply-tray-close').click();
 type('/'); composer.reset(); assert.equal(tray.classList.contains('hidden'), true); assert.equal(input.value, '');
 type('Texto'); key('Enter', {shiftKey: true}); assert.equal(sent, 1, 'Shift+Enter does not submit');
 key('Enter', {isComposing: true}); assert.equal(sent, 1, 'IME composition must not submit');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestQuickRepliesEditorInteractions(t *testing.T) {
	runControlDOMTest(t, QuickReplies(true), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/quick-replies'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {}; w.confirm = () => true;
let stored = [], requests = [];
w.fetch = async (url, options) => {
 const body = options.body ? JSON.parse(options.body) : null; requests.push({url, method: options.method, body});
 if (options.method === 'GET') return {ok: true, status: 200, json: async () => ({data: stored})};
 if (options.method === 'DELETE') { stored = []; return {ok: true, status: 204}; }
 const reply = {...body, id: 4, revision: options.method === 'PUT' ? body.revision + 1 : 1}; stored = [reply];
 return {ok: true, status: 200, json: async () => ({data: reply})};
};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes("const page = document.getElementById('quick-replies-page')")) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick();
 const form = d.getElementById('quick-reply-form');
 d.getElementById('quick-reply-shortcut').value = 'ola'; d.getElementById('quick-reply-title').value = 'Saudação';
 d.getElementById('quick-reply-content').value = 'Olá!\nTudo bem?'; d.getElementById('quick-reply-content').dispatchEvent(new w.Event('input'));
 assert.equal(d.getElementById('quick-reply-live-preview').textContent, 'Olá!\nTudo bem?');
 form.requestSubmit(); await tick();
 assert.equal(requests.find(item => item.method === 'POST').body.content, 'Olá!\nTudo bem?');
 assert.ok(d.getElementById('quick-replies-list').textContent.includes('/ola'));
 Array.from(d.querySelectorAll('#quick-replies-list button')).find(button => button.textContent === 'Editar').click();
 d.getElementById('quick-reply-content').value = 'Mensagem atualizada'; form.requestSubmit(); await tick();
 const update = requests.find(item => item.method === 'PUT'); assert.equal(update.body.revision, 1); assert.ok(update.url.endsWith('/4'));
 d.getElementById('quick-replies-search').value = 'não existe'; d.getElementById('quick-replies-search').dispatchEvent(new w.Event('input'));
 assert.equal(d.getElementById('quick-replies-list').children.length, 0);
 d.getElementById('quick-replies-search').value = ''; d.getElementById('quick-replies-search').dispatchEvent(new w.Event('input'));
 Array.from(d.querySelectorAll('#quick-replies-list button')).find(button => button.textContent === 'Excluir').click(); await tick();
 assert.equal(stored.length, 0); assert.ok(requests.some(item => item.method === 'DELETE'));
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestQuickReplyTrayRecoversFromLoadFailure(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID}
	runControlDOMTest(t, MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, nil, nil, repo.ConversationScopeCounts{}), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/messages/test'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
let failed = true, replies = [{id: 1, shortcut: 'ola', title: 'Olá', content: 'Olá!'}];
w.fetch = async () => ({ok: !failed, json: async () => failed ? {error: 'Falha temporária'} : {data: replies}});
for (const script of d.querySelectorAll('script')) if (script.textContent.includes("const tray = document.getElementById('quick-reply-tray')")) w.eval(script.textContent);
const input = d.querySelector('#message-composer [name=message]'); input.value = '/'; input.setSelectionRange(1, 1); input.dispatchEvent(new w.Event('input'));
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick(); assert.equal(d.getElementById('quick-reply-tray-status').textContent, 'Falha temporária'); assert.equal(d.getElementById('quick-reply-tray-insert').disabled, true);
 failed = false; d.getElementById('quick-reply-tray-retry').click(); await tick();
 assert.equal(d.getElementById('quick-reply-tray-content').textContent, 'Olá!');
 replies = [{id: 2, shortcut: 'novo', title: 'Nova resposta', content: 'Mensagem recém-cadastrada'}];
 d.getElementById('quick-reply-tray-refresh').click(); await tick();
 assert.equal(d.getElementById('quick-reply-tray-content').textContent, 'Mensagem recém-cadastrada');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
