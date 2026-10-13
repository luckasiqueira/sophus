package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"

	"github.com/a-h/templ"
)

// Run these interaction checks with ZUBLY_TEST_JSDOM pointing to an installed
// jsdom module. The application itself needs no JavaScript package dependency.
func runControlDOMTest(t *testing.T, component templ.Component, script string) {
	t.Helper()
	if os.Getenv("ZUBLY_TEST_JSDOM") == "" {
		t.Skip("set ZUBLY_TEST_JSDOM to run DOM interaction checks")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var html strings.Builder
	if err := component.Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", script)
	cmd.Stdin = strings.NewReader(html.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("DOM interaction check: %v\n%s", err, output)
	}
}

func TestReportCalendarInteractions(t *testing.T) {
	runControlDOMTest(t, Reports(repo.ConversationReport{Filter: repo.ReportFilter{
		Period: "custom", Timezone: "America/Sao_Paulo",
		From: time.Date(2024, 2, 27, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC),
	}}, ""), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), { runScripts: 'outside-only', url: 'https://zubly.test/reports' });
const w = dom.window, d = w.document;
w.CSS = { escape: value => value };
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('function enhanceAdminSelect(') || script.textContent.includes("const period = document.getElementById('report-period')") || script.textContent.includes("const calendar = document.getElementById('report-calendar')")) w.eval(script.textContent);
const from = d.getElementById('report-from'), to = d.getElementById('report-to'), calendar = d.getElementById('report-calendar');
const trigger = input => input.closest('[data-report-date]').querySelector('[data-calendar-trigger]');
const day = value => calendar.querySelector('[data-date="' + value + '"]');
trigger(from).click();
assert.equal(calendar.hidden, false);
assert.equal(trigger(from).getAttribute('aria-expanded'), 'true');
assert.equal(day('2024-02-27').getAttribute('aria-selected'), 'true');
assert.ok(day('2024-02-29'), 'leap day must exist');
assert.ok(day('2024-02-28').className.includes('bg-purple-100'), 'selected interval is highlighted');
day('2024-02-29').focus();
day('2024-02-29').dispatchEvent(new w.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
assert.equal(d.activeElement.dataset.date, '2024-03-01', 'keyboard crosses month boundary');
calendar.querySelector('[data-calendar-prev]').click();
day('2024-02-29').click();
assert.equal(from.value, '2024-02-29');
assert.equal(d.getElementById('report-period').value, 'custom');
assert.equal(to.min, from.value);
assert.equal(calendar.hidden, true);
assert.equal(d.activeElement, trigger(from));
trigger(to).click();
calendar.querySelector('[data-calendar-prev]').click();
assert.equal(day('2024-02-28').disabled, true, 'end dates before start are disabled');
day('2024-02-29').click();
assert.equal(to.value, '2024-02-29');
trigger(from).click();
calendar.querySelector('[data-calendar-next]').click();
day('2024-03-15').click();
assert.equal(from.value, '2024-03-15');
assert.equal(to.value, '2024-03-15', 'moving start past end adjusts the end');
assert.equal(new w.FormData(d.getElementById('report-filters')).get('from'), '2024-03-15');
trigger(to).click();
calendar.dispatchEvent(new w.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
assert.equal(calendar.hidden, true);
assert.equal(trigger(to).getAttribute('aria-expanded'), 'false');
trigger(from).click();
calendar.querySelector('[data-calendar-clear]').click();
assert.equal(from.value, '');
assert.equal(from.required, true);
trigger(from).click();
calendar.querySelector('[data-calendar-today]').click();
const parts = new w.Intl.DateTimeFormat('en-CA', { timeZone: 'America/Sao_Paulo', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date());
const part = name => parts.find(p => p.type === name).value;
assert.equal(from.value, part('year') + '-' + part('month') + '-' + part('day'));
dom.window.close();
`)
}

func TestContactsConnectionDropdownInteractions(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), { runScripts: 'outside-only', url: 'https://zubly.test/contacts' });
const w = dom.window, d = w.document, requests = [];
w.CSS = { escape: value => value };
w.HTMLElement.prototype.scrollIntoView = function () {};
w.fetch = async url => { requests.push(String(url)); return { ok: true, status: 200, text: async () => JSON.stringify({ data: String(url).includes('conversation-picker/connections') ? [{id: 5, name: 'Principal'}, {id: 8, name: 'Suporte'}] : [], total: 0, page: 1 }) }; };
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('function enhanceAdminSelect(') || script.textContent.includes('window.contactRequest =') || script.textContent.includes('const contactsState =')) w.eval(script.textContent);
(async () => {
 await new Promise(resolve => setTimeout(resolve, 30));
 const select = d.getElementById('contacts-connection');
 assert.equal(select.dataset.adminEnhanced, 'true');
 assert.equal(select._adminTrigger.textContent, 'Todas as conexões');
 select._adminTrigger.click();
 const menu = d.getElementById(select._adminTrigger.getAttribute('aria-controls'));
 const option = Array.from(menu.querySelectorAll('button')).find(button => button.textContent === 'Suporte');
 assert.ok(option, 'asynchronously loaded connections must appear in the dropdown');
 option.click();
 await new Promise(resolve => setTimeout(resolve, 10));
 assert.equal(select.value, '8');
 assert.equal(select._adminTrigger.textContent, 'Suporte');
 assert.ok(requests.some(url => url.includes('connectionId=8')), 'selection must filter contacts');
 assert.equal(menu.querySelector('[aria-selected="true"]').textContent, 'Suporte');
 d.getElementById('contacts-import').click();
 await new Promise(resolve => setTimeout(resolve, 10));
 const importSelect = d.getElementById('contact-import-connection');
 assert.equal(importSelect.dataset.adminEnhanced, 'true');
 assert.equal(importSelect._adminTrigger.textContent, 'Principal');
 dom.window.close();
})().catch(error => { console.error(error); process.exitCode = 1; dom.window.close(); });
`)
}
