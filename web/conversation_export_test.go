package web

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestConversationExportControlsAreAdminOnly(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusClosed, AgentID: &agentID, Contact: repo.Contact{Id: 10}}
	for _, role := range []string{repo.RoleAdmin, repo.RoleAgent} {
		var html strings.Builder
		if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3, Role: role}, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html.String(), `id="conversation-export-menu"`) != (role == repo.RoleAdmin) {
			t.Fatalf("export controls visibility for role %s", role)
		}
		if role == repo.RoleAdmin {
			for _, want := range []string{"Exportar conversa atual", "Exportar todas as conversas do contato", "/export/contact"} {
				if !strings.Contains(html.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
		}
	}
}

func TestConversationExportDownloadAndErrors(t *testing.T) {
	runControlDOMTest(t, ConversationExportControls("00000000-0000-0000-0000-000000000101", 10), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/messages/test'});
const w = dom.window, d = w.document, downloads = [], requests = [];
w.URL.createObjectURL = () => 'blob:export'; w.URL.revokeObjectURL = () => {};
w.HTMLAnchorElement.prototype.click = function () { downloads.push(this.download); };
let denied = false;
w.fetch = async (url, options) => { requests.push({url, options}); return {ok: !denied, status: denied ? 403 : 200, headers: {get: () => denied ? 'application/json' : 'application/pdf'}, json: async () => ({}), blob: async () => new w.Blob(['%PDF-1.4'])}; };
for (const script of d.querySelectorAll('script')) w.eval(script.textContent);
const buttons = d.querySelectorAll('[data-conversation-export]');
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 buttons[0].click(); assert.equal(buttons[1].disabled, true); await tick();
 assert.equal(downloads[0], 'conversa-00000000-0000-0000-0000-000000000101.pdf');
 buttons[1].click(); await tick(); assert.equal(downloads[1], 'conversas-contato-10.pdf'); assert.ok(requests[1].url.endsWith('/export/contact')); assert.equal(requests[1].options.credentials, 'same-origin');
 denied = true; buttons[0].click(); await tick(); assert.equal(downloads.length, 2);
 const error = d.getElementById('conversation-export-error'); assert.equal(error.classList.contains('hidden'), false); assert.ok(error.textContent.includes('administradores')); assert.equal(d.getElementById('conversation-export-menu').open, true); assert.equal(buttons[0].disabled, false);
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
