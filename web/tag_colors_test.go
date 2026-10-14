package web

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestTagColorStylesMatchStoredColors(t *testing.T) {
	if len(tagColorStyles) != len(repo.ContactTagColors) {
		t.Fatalf("%d styles for %d stored colors", len(tagColorStyles), len(repo.ContactTagColors))
	}
	migration, err := os.ReadFile("../internal/repo/migrations/89__add_company_tag_colors.sql")
	if err != nil {
		t.Fatal(err)
	}
	for index, color := range repo.ContactTagColors {
		if tagColorStyles[index].Key != color {
			t.Fatalf("style %d = %q, want %q", index, tagColorStyles[index].Key, color)
		}
		if !strings.Contains(string(migration), "'"+color+"'") {
			t.Fatalf("migration constraint does not allow %q", color)
		}
	}
	if got := tagColorStyle("unknown").Key; got != repo.DefaultContactTagColor {
		t.Fatalf("unknown color falls back to %q", got)
	}
}

func TestConversationCardShowsColoredTagChips(t *testing.T) {
	conversation := repo.Conversation{URL: uuid.New(), Contact: repo.Contact{Id: 9, Name: "Cliente"}, Tags: []repo.ConversationTag{
		{Name: "VIP", Color: "amber"}, {Name: "Cliente recorrente", Color: "teal"}, {Name: "Atacado", Color: "pink"}, {Name: "Sul", Color: "blue"},
	}}
	var output bytes.Buffer
	if err := ConversationList([]repo.Conversation{conversation}, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"data-conversation-tags", "bg-amber-100 text-amber-800", "bg-amber-500", ">VIP</span>", "bg-teal-100 text-teal-800", ">Cliente recorrente</span>", ">+2</span>", `title="Atacado, Sul"`, `aria-label="Tags: VIP, Cliente recorrente, Atacado, Sul"`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("conversation card does not contain %q: %s", expected, html)
		}
	}
	if strings.Contains(html, ">Atacado</span>") {
		t.Fatal("conversation card must show at most two tag chips")
	}

	output.Reset()
	conversation.Tags = nil
	if err := ConversationList([]repo.Conversation{conversation}, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "data-conversation-tags") {
		t.Fatal("conversation without tags must not render the tag row")
	}
}

func TestContactTagColorPickerInteractions(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/contacts'});
const w = dom.window, d = w.document; w.HTMLElement.prototype.scrollIntoView = function () {};
let requests = [];
const contact = {id: 1, name: 'Ana', number: '5511911111111', tags: ['VIP'], tagIds: [7], tagColors: ['amber']};
const response = (status, payload) => ({ok: status >= 200 && status < 300, status, text: async () => payload === undefined ? '' : JSON.stringify(payload)});
w.fetch = async (url, options = {}) => {
 const method = options.method || 'GET';
 requests.push({url, method, body: options.body ? JSON.parse(options.body) : null});
 if (url.endsWith('/contact-tags') && method === 'GET') return response(200, {data: [{id: 7, name: 'VIP', color: 'amber', contactCount: 1}]});
 if (method !== 'GET') return response(200, {data: {}});
 if (url.includes('/contacts?')) return response(200, {data: [contact], page: 1, total: 1});
 return response(200, {data: []});
};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
(async () => {
 await tick(); await tick();
 assert.ok(d.querySelector('#contacts-table .bg-amber-100'), 'contact list chip uses the tag color');
 assert.equal(d.querySelectorAll('#contact-tag-create-colors input[type="radio"]').length, 9);
 assert.notEqual(d.querySelector('#contact-tag-create-colors input:checked').value, 'amber', 'new tags default to an unused color');
 d.querySelector('#contact-tag-create-colors input[value="teal"]').checked = true;
 d.getElementById('contact-tag-name').value = 'Lead'; d.getElementById('contact-tag-create-form').requestSubmit(); await tick(); await tick();
 assert.deepEqual(requests.find(r => r.method === 'POST').body, {name: 'Lead', color: 'teal'});
 Array.from(d.querySelectorAll('#contact-tags-list button')).find(button => button.textContent.includes('Editar')).click();
 assert.equal(d.querySelector('#contact-tag-edit-colors input:checked').value, 'amber');
 d.querySelector('#contact-tag-edit-colors input[value="red"]').checked = true;
 d.getElementById('contact-tag-edit-form').requestSubmit(); await tick(); await tick();
 assert.deepEqual(requests.find(r => r.method === 'PUT').body, {name: 'VIP', color: 'red'}, 'color-only change is saved');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
