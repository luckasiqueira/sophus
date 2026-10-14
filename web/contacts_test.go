package web

import (
	"github.com/google/uuid"
	"sophus/internal/repo"
	"testing"
)

func TestContactEditAndTagErrorInteractions(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/contacts'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
const contact = {id: 1, name: 'João', number: '5511999999999', email: '', company: '', tagIds: [], tags: []};
let requests = [], editFailure = '', createFailure = false, refreshFailure = false;
const response = (status, payload) => ({ok: status >= 200 && status < 300, status, text: async () => payload === undefined ? '' : JSON.stringify(payload)});
w.fetch = async (url, options = {}) => {
 requests.push({url, method: options.method || 'GET', body: options.body ? JSON.parse(options.body) : null});
 if (options.method === 'PUT' && url.endsWith('/contacts/1')) return editFailure ? response(409, {error: editFailure}) : response(204);
 if (options.method === 'PUT' && url.endsWith('/tags')) return response(204);
 if (options.method === 'POST') return createFailure ? response(503) : response(201, {data: {id: 2, name: 'Nova'}});
 if (url.includes('/contacts?')) return refreshFailure ? response(500, {error: 'Falha ao pesquisar'}) : response(200, {data: [contact], page: 1, total: 1});
 if (url.endsWith('/contact-tags')) return response(200, {data: [{id: 2, name: 'VIP', contactCount: 0}]});
 return response(200, {data: []});
};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick(); w.openContactEditor(contact);
 const form = d.getElementById('contact-edit-form');
 d.querySelector('#contact-edit-tags input').checked = true; form.requestSubmit(); await tick();
 assert.ok(requests.some(r => r.method === 'PUT' && r.url.endsWith('/contacts/1/tags')));
 assert.equal(requests.some(r => r.method === 'PUT' && r.url.endsWith('/contacts/1')), false, 'tag-only change must not rewrite phone/name');
 assert.equal(d.getElementById('contact-edit-modal').classList.contains('hidden'), true);
 w.openContactEditor(contact); d.getElementById('contact-edit-name').value = 'Nome alterado'; editFailure = 'já existe um contato com este telefone'; form.requestSubmit(); await tick();
 const error = d.getElementById('contact-edit-error');
 assert.equal(error.textContent, editFailure); assert.equal(error.classList.contains('hidden'), false); assert.equal(d.getElementById('contact-edit-modal').classList.contains('hidden'), false); assert.equal(d.getElementById('contact-edit-save').disabled, false);
 editFailure = ''; refreshFailure = true; form.requestSubmit(); await tick();
 assert.equal(d.getElementById('contact-edit-modal').classList.contains('hidden'), true);
 assert.ok(d.getElementById('contacts-error').textContent.includes('O contato foi salvo'));
 assert.equal(d.getElementById('contacts-error').classList.contains('hidden'), false, 'refresh error must not be shown in closed modal');
 refreshFailure = false; d.querySelector('[data-contacts-tab="tags"]').click(); createFailure = true;
 d.getElementById('contact-tag-name').value = 'Nova'; d.getElementById('contact-tag-create-form').requestSubmit(); await tick();
 const tagError = d.getElementById('contact-tag-create-error');
 assert.ok(tagError.textContent.includes('HTTP 503'), 'empty server response needs a useful fallback'); assert.equal(tagError.classList.contains('hidden'), false); assert.equal(d.getElementById('contact-tag-name').value, 'Nova'); assert.equal(d.querySelector('#contact-tag-create-form button').disabled, false);
 createFailure = false; d.getElementById('contact-tag-create-form').requestSubmit(); await tick();
 assert.equal(d.getElementById('contact-tag-name').value, ''); assert.equal(tagError.classList.contains('hidden'), true);
 w.fetch = async () => response(401);
 await assert.rejects(() => w.contactRequest('POST', '/management/api/contact-tags', {name: 'Teste'}), /sessão expirou/);
 w.fetch = async () => ({ok: false, status: 500, text: async () => 'Falha de serviço'});
 await assert.rejects(() => w.contactRequest('PUT', '/management/api/contacts/1', {}), /Falha de serviço/);
 w.fetch = async () => { throw new Error('network'); };
 await assert.rejects(() => w.contactRequest('POST', '/management/api/contact-tags', {}), /conectar ao servidor/);
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestConversationContactEditDisplaysEmptyResponseErrors(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID, Contact: repo.Contact{Id: 1, Name: "João", Number: "5511999999999"}}
	runControlDOMTest(t, MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3, Role: repo.RoleAdmin}, nil, nil, nil, repo.ConversationScopeCounts{}), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/messages/test'});
const w = dom.window, d = w.document; w.conversationDocument = d; w.HTMLElement.prototype.scrollIntoView = function () {};
w.fetch = async () => ({ok: false, status: 403, text: async () => ''});
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes("const modal = document.getElementById('conversation-contact-edit-modal')")) w.eval(script.textContent);
d.getElementById('conversation-contact-edit-button').click(); d.getElementById('conversation-contact-edit-name').value = 'Novo nome'; d.getElementById('conversation-contact-edit-form').requestSubmit();
setTimeout(() => {
 try { const box = d.getElementById('conversation-contact-edit-error'); assert.equal(box.classList.contains('hidden'), false); assert.ok(box.textContent.includes('não foi autorizada')); assert.equal(d.getElementById('conversation-contact-edit-save').disabled, false); dom.window.close(); }
 catch(error) { console.error(error); process.exitCode = 1; dom.window.close(); }
}, 0);
`)
}

func TestContactTagFilterAndTagContactList(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/contacts'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
const vip = {id: 1, name: 'Ana', number: '5511911111111', tags: ['VIP'], tagIds: [7]};
let requests = [], tags = [{id: 7, name: 'VIP', contactCount: 51}, {id: 8, name: 'Lead', contactCount: 0}];
const response = (status, payload) => ({ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(payload)});
w.fetch = async url => {
 requests.push(url);
 if (url.endsWith('/contact-tags')) return response(200, {data: tags});
 if (url.includes('/contacts?')) { const params = new URL(url, 'https://zubly.test').searchParams; const page = Number(params.get('page')); return response(200, {data: params.get('tagId') ? [Object.assign({}, vip, {id: page})] : [], page, pageSize: 50, total: params.get('tagId') ? 51 : 0}); }
 return response(200, {data: []});
};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const lastContacts = () => new URL(requests.filter(url => url.includes('/contacts?')).pop(), 'https://zubly.test').searchParams;
(async () => {
 await tick(); await tick();
 const filter = d.getElementById('contacts-tag');
 assert.deepEqual(Array.from(filter.options).map(option => option.textContent), ['Todas as tags', 'VIP', 'Lead']);
 filter.value = '7'; filter.dispatchEvent(new w.Event('change')); await tick();
 assert.equal(lastContacts().get('tagId'), '7'); assert.equal(lastContacts().get('page'), '1');
 d.querySelector('[data-contacts-tab="tags"]').click();
 assert.equal(d.querySelector('[data-tag-contacts-toggle="8"]').disabled, true, 'empty tag cannot be expanded');
 d.querySelector('[data-tag-contacts-toggle="7"]').click(); await tick(); await tick();
 assert.equal(d.querySelector('[data-tag-contacts-toggle="7"]').getAttribute('aria-expanded'), 'true');
 assert.equal(d.querySelectorAll('#contact-tag-contacts-7 li').length, 1);
 assert.ok(d.getElementById('contact-tag-contacts-7').textContent.includes('Mostrando 1 de 51'));
 Array.from(d.querySelectorAll('#contact-tag-contacts-7 button')).find(button => button.textContent.includes('Carregar mais')).click(); await tick(); await tick();
 assert.equal(lastContacts().get('page'), '2'); assert.equal(d.querySelectorAll('#contact-tag-contacts-7 li').length, 2);
 filter.value = ''; Array.from(d.querySelectorAll('#contact-tag-contacts-7 button')).find(button => button.textContent.includes('Abrir na lista')).click(); await tick();
 assert.equal(filter.value, '7'); assert.equal(d.getElementById('contacts-panel').classList.contains('hidden'), false); assert.equal(lastContacts().get('tagId'), '7');
 tags = [{id: 8, name: 'Lead', contactCount: 0}]; await w.loadTags(); await tick();
 assert.equal(filter.value, '', 'deleted tag must clear the filter'); assert.equal(lastContacts().get('tagId'), null);
 assert.equal(d.getElementById('contact-tag-contacts-7'), null);
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
