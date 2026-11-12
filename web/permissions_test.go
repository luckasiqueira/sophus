package web

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/uuid"

	"sophus/internal/repo"
)

func adminRenderContext() context.Context {
	return repo.ContextWithAgent(context.Background(), repo.Agent{Id: 1, CompanyId: 3, Role: repo.RoleAdmin})
}

func agentRenderContext() context.Context {
	return repo.ContextWithAgent(context.Background(), repo.Agent{Id: 2, CompanyId: 3, Role: repo.RoleAgent})
}

func supervisorRenderContext() context.Context {
	return repo.ContextWithAgent(context.Background(), repo.Agent{Id: 4, CompanyId: 3, Role: repo.RoleSupervisor})
}

func renderWith(t *testing.T, ctx context.Context, component templ.Component) string {
	t.Helper()
	var html strings.Builder
	if err := component.Render(ctx, &html); err != nil {
		t.Fatal(err)
	}
	return html.String()
}

func TestSidebarHidesManagementMenusFromCommonAgents(t *testing.T) {
	// Match the sidebar anchors, not the onboarding tour's selector strings.
	managed := []string{`href="/flows/" title=`, `href="/ai-agents" title=`, `href="/agents" title=`, `href="/reports" title=`, `href="/settings" title=`}
	visible := []string{`href="/contacts" title=`, `href="/instances/" title=`, `href="/media" title=`, `href="/preferences"`}
	agentHTML := renderWith(t, agentRenderContext(), MediaLibrary())
	adminHTML := renderWith(t, adminRenderContext(), MediaLibrary())
	for _, link := range managed {
		if strings.Contains(agentHTML, link) {
			t.Errorf("common agent sees %s", link)
		}
		if !strings.Contains(adminHTML, link) {
			t.Errorf("admin lost %s", link)
		}
	}
	for _, link := range visible {
		if !strings.Contains(agentHTML, link) {
			t.Errorf("common agent lost %s", link)
		}
	}
	if anonymous := renderWith(t, context.Background(), MediaLibrary()); !strings.Contains(anonymous, `href="/contacts" title=`) || strings.Contains(anonymous, `href="/flows/" title=`) {
		t.Error("pages rendered without an agent must fail closed")
	}
}

func TestMediaLibraryHidesDeletionFromCommonAgents(t *testing.T) {
	html := renderWith(t, agentRenderContext(), MediaLibrary())
	if !strings.Contains(html, `id="media-preview-delete" class="grid h-10 w-10 place-items-center rounded-xl bg-red-500/15 text-red-300 hover:bg-red-500/25 hidden"`) {
		t.Error("media delete button must be hidden for common agents")
	}
	if strings.Contains(html, "Ver upgrades") {
		t.Error("billing upgrade link must be hidden for common agents")
	}
}

func TestContactsHidesTagsImportAndDeletionFromCommonAgents(t *testing.T) {
	agentHTML := renderWith(t, agentRenderContext(), Contacts())
	if !strings.Contains(agentHTML, `data-contacts-tab="tags" class="rounded-lg px-4 py-2 text-sm font-bold text-gray-600 hidden"`) {
		t.Error("tags tab must be hidden for common agents")
	}
	if !strings.Contains(agentHTML, `<script id="contacts-can-manage" type="application/json">false`) {
		t.Error("contact deletion flag must be false for common agents")
	}
	if !strings.Contains(renderWith(t, adminRenderContext(), Contacts()), `<script id="contacts-can-manage" type="application/json">true`) {
		t.Error("admins keep contact deletion")
	}
}

func TestInstancesAreViewOnlyForCommonAgents(t *testing.T) {
	connections := []repo.ConnectionEVO{
		{Id: 7, Name: "Atendimento", Status: repo.ConnectionStatusConnected},
		{Id: 8, Name: "Vendas", Status: repo.ConnectionStatusDisconnected},
	}
	html := renderWith(t, agentRenderContext(), Instances(connections))
	for _, forbidden := range []string{`hx-get="/instances/create"`, `hx-post="/instances/8/reconnect"`, `data-instance-rename="/instances/7"`, `data-instance-disconnect="/instances/7/disconnect"`, `data-instance-delete="/instances/8"`, `data-import-contacts="/instances/7/contacts/import"`} {
		if strings.Contains(html, forbidden) {
			t.Errorf("common agent sees %s", forbidden)
		}
	}
	if !strings.Contains(html, "Atendimento") || !strings.Contains(html, "Vendas") || !strings.Contains(html, "Desconectada") {
		t.Error("common agent must still see instances and their status")
	}
}

func TestContactsDOMOmitsDeletionForCommonAgents(t *testing.T) {
	asAgent := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return Contacts().Render(repo.ContextWithAgent(ctx, repo.Agent{Id: 2, CompanyId: 3, Role: repo.RoleAgent}), w)
	})
	runControlDOMTest(t, asAgent, `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/contacts'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
const contact = {id: 1, name: 'Pessoa Teste', number: '5511900000000', tagIds: [], tags: []};
const response = (status, payload) => ({ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(payload)});
w.fetch = async url => url.includes('/contacts?') ? response(200, {data: [contact], page: 1, total: 1}) : response(200, {data: []});
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick(); await tick();
 assert.ok(d.querySelector('#contacts-table button[aria-label^="Editar"], #contacts-table button'), 'contact row renders actions');
 assert.equal(d.querySelectorAll('[aria-label^="Excluir contato"]').length, 0, 'no delete buttons for common agents');
 w.openContactEditor(contact);
 const editorDelete = Array.from(d.querySelectorAll('#contact-edit-form button')).find(b => b.textContent.includes('Excluir contato'));
 assert.ok(editorDelete.classList.contains('hidden'));
 d.querySelector('[data-contacts-tab="tags"]').click();
 assert.equal(d.getElementById('contact-tags-panel').classList.contains('hidden'), true, 'tags panel stays closed');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}

func TestSupervisorSeesOperationalButNotAdministrativeMenus(t *testing.T) {
	html := renderWith(t, supervisorRenderContext(), Contacts())
	for _, link := range []string{`href="/flows/" title=`, `href="/ai-agents" title=`, `href="/reports" title=`, `href="/instances/" title=`} {
		if !strings.Contains(html, link) {
			t.Errorf("supervisor lost %s", link)
		}
	}
	for _, link := range []string{`href="/agents" title=`, `href="/settings" title=`} {
		if strings.Contains(html, link) {
			t.Errorf("supervisor sees admin-only %s", link)
		}
	}
	if !strings.Contains(html, `<script id="contacts-can-manage" type="application/json">true`) {
		t.Error("supervisors delete contacts and manage tags")
	}
	if strings.Contains(html, `data-contacts-tab="tags" class="rounded-lg px-4 py-2 text-sm font-bold text-gray-600 hidden"`) {
		t.Error("supervisors see the tags tab")
	}
	media := renderWith(t, supervisorRenderContext(), MediaLibrary())
	if !strings.Contains(media, `bg-red-500/15 text-red-300 hover:bg-red-500/25 hidden"`) {
		t.Error("media deletion stays admin-only")
	}
	instances := renderWith(t, supervisorRenderContext(), Instances([]repo.ConnectionEVO{{Id: 8, Name: "Vendas", Status: repo.ConnectionStatusDisconnected}}))
	for _, action := range []string{`hx-get="/instances/create"`, `hx-post="/instances/8/reconnect"`, `data-instance-delete="/instances/8"`} {
		if !strings.Contains(instances, action) {
			t.Errorf("supervisor lost instance action %s", action)
		}
	}
}

func TestSupervisorHandlesConversationsOfOtherAgents(t *testing.T) {
	owner := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &owner,
		Contact: repo.Contact{Id: 1, Name: "Pessoa Teste", Number: "5511900000000"}}
	render := func(role string) string {
		agent := repo.Agent{Id: 9, CompanyId: 3, Role: role}
		return renderWith(t, repo.ContextWithAgent(context.Background(), agent),
			MessageSingle(conversation, agent, nil, nil, nil, repo.ConversationScopeCounts{}))
	}
	supervisor, common := render(repo.RoleSupervisor), render(repo.RoleAgent)
	for _, marker := range []string{"openTransferConversationModal()", `id="message-composer"`, "/export"} {
		if !strings.Contains(supervisor, marker) {
			t.Errorf("supervisor lost %s on another agent's conversation", marker)
		}
	}
	if strings.Contains(common, "openTransferConversationModal()") || strings.Contains(common, `id="message-composer"`) {
		t.Error("common agents must not handle other agents' conversations")
	}
	if !canReplyToConversation(conversation, repo.Agent{Id: 9, Role: repo.RoleSupervisor}) || canReplyToConversation(conversation, repo.Agent{Id: 9, Role: repo.RoleAgent}) {
		t.Error("reply permission must follow the supervisor role")
	}
}

func TestCommonAgentsCanEditContactFromConversation(t *testing.T) {
	agent := repo.Agent{Id: 9, CompanyId: 3, Role: repo.RoleAgent}
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agent.Id,
		Contact: repo.Contact{Id: 1, Name: "Pessoa Teste", Number: "5511900000000"}}
	html := renderWith(t, repo.ContextWithAgent(context.Background(), agent),
		MessageSingle(conversation, agent, nil, nil, nil, repo.ConversationScopeCounts{}))
	if !strings.Contains(html, `id="conversation-contact-edit-button"`) || !strings.Contains(html, `id="conversation-contact-edit-modal"`) {
		t.Error("common agents edit contacts from the conversation")
	}
}

func TestContactsMobileCardsUseActionMenu(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/contacts'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
const contact = {id: 1, name: 'Pessoa Teste', number: '5511900000000', tagIds: [], tags: []};
const response = (status, payload) => ({ok: true, status, text: async () => JSON.stringify(payload)});
w.fetch = async url => url.includes('/contacts?') ? response(200, {data: [contact], page: 1, total: 1}) : response(200, {data: []});
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick(); await tick();
 const card = d.querySelector('#contacts-cards article');
 assert.ok(card.textContent.includes('5511900000000'), 'mobile card shows the phone');
 const toggle = card.querySelector('[data-contact-menu-toggle]'), menu = card.querySelector('[data-contact-menu]');
 assert.equal(card.querySelectorAll('button').length, 4, 'one toggle plus three menu items');
 assert.ok(menu.classList.contains('hidden'));
 toggle.click();
 assert.equal(menu.classList.contains('hidden'), false); assert.equal(toggle.getAttribute('aria-expanded'), 'true');
 assert.deepEqual(Array.from(menu.querySelectorAll('[role=menuitem]')).map(b => b.textContent.trim()), ['Editar', 'Conversar', 'Excluir']);
 d.body.click();
 assert.ok(menu.classList.contains('hidden'), 'clicking outside closes the menu');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
