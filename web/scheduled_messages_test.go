package web

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestScheduledMessagesPageInteractions(t *testing.T) {
	policy := ScheduleAttachmentPolicy{AllowedTypes: []string{"image", "document"}, MaxBytes: map[string]int64{"image": 1024, "document": 4096}}
	runControlDOMTest(t, ScheduledMessages("America/Sao_Paulo", true, policy, nil), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/schedules'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
w.URL.createObjectURL = () => 'blob:preview'; w.URL.revokeObjectURL = () => {};
const future = new Date(Date.now() + 3 * 86400000).toISOString();
const items = [
 {id: 1, contactId: 5, contactName: 'Cliente A', contactNumber: '5511900000001', connectionId: 2, connectionName: 'Comercial', agentName: 'Ana', body: 'Lembrete da reunião', mediaKind: 'document', mediaPath: 'a.pdf', mediaName: 'proposta.pdf', mediaSize: 2048, scheduledFor: future, status: 'pending', lastError: '', recurrence: 'weekly', recurrenceUntil: '2026-12-20'},
 {id: 2, contactId: 6, contactName: 'Cliente B', contactNumber: '5511900000002', connectionId: 2, connectionName: 'Comercial', body: 'Oi', scheduledFor: future, status: 'failed', lastError: 'a conexão Comercial estava desconectada no horário do envio'},
 {id: 3, contactId: 7, contactName: 'Cliente C', contactNumber: '5511900000003', connectionId: 2, connectionName: 'Comercial', body: 'Enviada', scheduledFor: future, sentAt: future, status: 'sent', conversationURL: '00000000-0000-0000-0000-000000000001'},
];
let requests = [];
const response = (status, payload) => ({ok: status >= 200 && status < 300, status, text: async () => payload === undefined ? '' : JSON.stringify(payload)});
w.fetch = async (url, options = {}) => {
 const method = options.method || 'GET'; requests.push({url, method, body: options.body});
 if (url.startsWith('/schedules/api?')) return response(200, {data: items, total: 3, page: 1, pageSize: 30, counts: {pending: 1, failed: 1, sent: 1}});
 if (url.includes('/conversation-picker/connections')) return response(200, {data: [{id: 2, name: 'Comercial', number: '551130000000'}]});
 if (url.includes('/management/api/contacts?')) return response(200, {data: [{id: 5, name: 'Cliente A', number: '5511900000001'}, {id: 9, name: 'Grupo', number: '1203', isGroup: true}]});
 if (method === 'POST' || method === 'PUT') return response(url.endsWith('/send-now') ? 200 : 201, {data: {id: 10}});
 if (method === 'DELETE') return response(204);
 return response(404, {error: 'não encontrado'});
};
w.confirmAction = async () => true;
for (const script of d.querySelectorAll('script')) if (script.textContent.includes("getElementById('schedules-page')")) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
const button = (row, label) => Array.from(row.querySelectorAll('button')).find(item => item.textContent.includes(label));
(async () => {
 await settle();
 assert.equal(new URL(requests[0].url, 'https://zubly.test').searchParams.get('status'), 'upcoming');
 const rows = d.querySelectorAll('#schedules-list article');
 assert.equal(rows.length, 3);
 assert.ok(rows[0].textContent.includes('Agendada') && rows[0].textContent.includes('proposta.pdf') && rows[0].textContent.includes('por Ana'));
 assert.ok(rows[1].textContent.includes('Falhou') && rows[1].textContent.includes('desconectada') && rows[1].textContent.includes('Reagendar'));
 assert.equal(rows[2].querySelector('a').getAttribute('href'), '/messages/00000000-0000-0000-0000-000000000001');
 assert.equal(rows[2].textContent.includes('Cancelar'), false, 'sent schedules cannot be canceled');
 assert.ok(rows[0].textContent.includes('Repete toda semana até 20/12/2026'), 'recurrence is shown on the schedule');
 assert.equal(d.querySelector('[data-schedule-count="upcoming"]').textContent, '1');
 assert.ok(d.querySelector('[data-schedule-count="failed"]').className.includes('bg-red-100'));
 assert.ok(d.querySelector('[data-schedule-count="canceled"]').classList.contains('hidden'), 'empty counters stay hidden');
 button(rows[0], 'Enviar agora').click(); await settle();
 assert.ok(requests.some(r => r.method === 'POST' && r.url === '/schedules/api/1/send-now'));

 d.getElementById('schedule-new').click(); await settle();
 assert.equal(d.getElementById('schedule-modal').classList.contains('hidden'), false);
 assert.equal(d.getElementById('schedule-connection').value, '2', 'single connection is preselected');
 const results = d.querySelectorAll('#schedule-contact-results button');
 assert.equal(results.length, 1, 'groups are not offered');
 d.getElementById('schedule-form').requestSubmit(); await settle();
 assert.ok(d.getElementById('schedule-form-error').textContent.includes('contato'));
 results[0].click();
 assert.equal(d.getElementById('schedule-contact-name').textContent, 'Cliente A');
 d.querySelector('[data-schedule-shortcut="tomorrow"]').click();
 assert.equal(d.getElementById('schedule-time').value, '09:00');
 const fileInput = d.getElementById('schedule-file');
 const choose = file => { Object.defineProperty(fileInput, 'files', {configurable: true, value: [file]}); fileInput.dispatchEvent(new w.Event('change')); };
 choose(new w.File([new Uint8Array(2048)], 'grande.png', {type: 'image/png'}));
 assert.ok(d.getElementById('schedule-form-error').textContent.includes('limite'), 'oversized image is rejected before upload');
 choose(new w.File([new Uint8Array(10)], 'clip.mp4', {type: 'video/mp4'}));
 assert.ok(d.getElementById('schedule-form-error').textContent.includes('desativados'), 'disabled kinds are rejected');
 choose(new w.File([new Uint8Array(100)], 'foto.png', {type: 'image/png'}));
 assert.equal(d.getElementById('schedule-file-name').textContent, 'foto.png');
 d.getElementById('schedule-body').value = 'Bom dia!'; d.getElementById('schedule-body').dispatchEvent(new w.Event('input'));
 const recurrence = d.getElementById('schedule-recurrence');
 assert.ok(d.getElementById('schedule-until-field').classList.contains('hidden'));
 recurrence.value = 'monthly'; recurrence.dispatchEvent(new w.Event('change'));
 assert.equal(d.getElementById('schedule-until-field').classList.contains('hidden'), false);
 assert.ok(d.getElementById('schedule-recurrence-hint').textContent.includes('último dia'));
 d.getElementById('schedule-until').value = '2027-06-30'; d.getElementById('schedule-until').dispatchEvent(new w.Event('change'));
 d.getElementById('schedule-form').requestSubmit(); await settle();
 const created = requests.find(r => r.method === 'POST' && r.url === '/schedules/api');
 assert.ok(created, 'schedule was submitted');
 const tomorrow = new Intl.DateTimeFormat('en-CA', {timeZone: 'America/Sao_Paulo', year: 'numeric', month: '2-digit', day: '2-digit'}).format(new Date(Date.now() + 86400000));
 assert.equal(created.body.get('date'), tomorrow); assert.equal(created.body.get('time'), '09:00');
 assert.equal(created.body.get('contactId'), '5'); assert.equal(created.body.get('connectionId'), '2');
 assert.equal(created.body.get('recurrence'), 'monthly'); assert.equal(created.body.get('recurrenceUntil'), '2027-06-30');
 assert.equal(created.body.get('body'), 'Bom dia!'); assert.equal(created.body.get('kind'), 'image'); assert.equal(created.body.get('file').name, 'foto.png');
 assert.equal(d.getElementById('schedule-modal').classList.contains('hidden'), true);

 button(d.querySelectorAll('#schedules-list article')[0], 'Editar').click(); await settle();
 assert.equal(d.getElementById('schedule-modal-title').textContent, 'Editar agendamento');
 assert.equal(d.getElementById('schedule-file-name').textContent, 'proposta.pdf');
 assert.equal(d.getElementById('schedule-recurrence').value, 'weekly'); assert.equal(d.getElementById('schedule-until').value, '2026-12-20');
 d.getElementById('schedule-file-remove').click();
 d.getElementById('schedule-form').requestSubmit(); await settle();
 const updated = requests.find(r => r.method === 'PUT');
 assert.ok(updated.url.endsWith('/schedules/api/1')); assert.equal(updated.body.get('removeMedia'), 'true');

 button(d.querySelectorAll('#schedules-list article')[0], 'Cancelar').click(); await settle();
 assert.ok(requests.some(r => r.method === 'DELETE' && r.url === '/schedules/api/1'));
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestScheduledMessagesOpensDraftFromConversation(t *testing.T) {
	policy := ScheduleAttachmentPolicy{AllowedTypes: []string{"document"}, MaxBytes: map[string]int64{"document": 4096}}
	draft := &ScheduleDraft{ContactId: 5, ContactName: "Cliente A", ContactNumber: "5511900000001", ConnectionId: 3}
	runControlDOMTest(t, ScheduledMessages("America/Sao_Paulo", false, policy, draft), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/schedules?contactId=5&connectionId=3'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
const response = (status, payload) => ({ok: true, status, text: async () => JSON.stringify(payload)});
let contactSearches = 0;
w.fetch = async url => {
 if (url.startsWith('/schedules/api?')) return response(200, {data: [], total: 0, page: 1, pageSize: 30, counts: {}});
 if (url.includes('/conversation-picker/connections')) return response(200, {data: [{id: 2, name: 'Comercial'}, {id: 3, name: 'Suporte'}]});
 if (url.includes('/management/api/contacts?')) { contactSearches++; return response(200, {data: []}); }
 return response(200, {});
};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes("getElementById('schedules-page')")) w.eval(script.textContent);
const settle = async () => { for (let i = 0; i < 6; i++) await new Promise(resolve => setTimeout(resolve, 0)); };
(async () => {
 await settle();
 assert.equal(d.getElementById('schedule-modal').classList.contains('hidden'), false, 'the form opens for the conversation contact');
 assert.equal(d.getElementById('schedule-contact-name').textContent, 'Cliente A');
 assert.equal(d.getElementById('schedule-connection').value, '3', 'the conversation connection is preselected');
 assert.equal(contactSearches, 0, 'no contact search is needed');
 assert.equal(w.location.search, '', 'ids are removed from the address bar');
 assert.equal(d.querySelector('#schedules-list'), d.getElementById('schedules-list'));
 d.querySelector('[data-schedule-close]').click();
 d.getElementById('schedule-new').click(); await settle();
 assert.equal(d.getElementById('schedule-contact-selected').classList.contains('hidden'), true, 'a later new schedule starts empty');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestConversationHeaderLinksToScheduleForContact(t *testing.T) {
	id := 7
	for _, group := range []bool{false, true} {
		var html strings.Builder
		conversation := repo.Conversation{URL: uuid.New(), AgentID: &id, Status: "open", ConnectionID: 3, Contact: repo.Contact{Id: 42, Name: "Cliente", IsGroup: group}}
		if err := ConversationPanel(conversation, repo.Agent{Id: id}, nil, nil).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		link := strings.Contains(html.String(), `href="/schedules?contactId=42&amp;connectionId=3"`)
		if link == group {
			t.Fatalf("group=%v: schedule link present=%v", group, link)
		}
	}
}
