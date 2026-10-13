package web

import "testing"

func TestContactAvatarRecoveryAndRealtimeUpdate(t *testing.T) {
	runControlDOMTest(t, ContactAvatarRuntime(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts: 'outside-only', url: 'https://zubly.test/messages/test'});
const w = dom.window, d = w.document, requests = [];
const image = d.createElement('img'); image.src = '/management/api/contacts/7/avatar'; d.body.append(image);
const other = d.createElement('img'); other.src = '/management/api/contacts/8/avatar'; d.body.append(other);
const unrelated = d.createElement('img'); unrelated.src = 'https://other.test/logo.png'; d.body.append(unrelated);
const listeners = {}; w.appConversationEvents = {addEventListener: (name, fn) => listeners[name] = fn};
w.fetch = async (url, options) => { requests.push({url, options}); return {ok: true, status: 204}; };
for (const script of d.querySelectorAll('script')) w.eval(script.textContent);
image.dispatchEvent(new w.Event('error'));
assert.ok(image.src.includes('fallback=1')); assert.equal(requests.length, 1); assert.equal(requests[0].url, '/management/api/contacts/7/avatar/refresh'); assert.equal(requests[0].options.method, 'POST');
const duplicate = d.createElement('img'); duplicate.src = '/management/api/contacts/7/avatar'; d.body.append(duplicate); duplicate.dispatchEvent(new w.Event('error')); assert.equal(requests.length, 1, 'same contact refresh is deduplicated');
image.dispatchEvent(new w.Event('error')); assert.ok(image.src.startsWith('data:image/svg+xml,')); image.dispatchEvent(new w.Event('error')); assert.equal(requests.length, 1, 'fallback does not loop');
listeners['contact-avatar']({data: JSON.stringify({contactId: 7})});
assert.ok(image.src.includes('/contacts/7/avatar?v=')); assert.ok(duplicate.src.includes('/contacts/7/avatar?v=')); assert.equal(image.dataset.avatarFinalFallback, undefined); assert.ok(!other.src.includes('?v='));
unrelated.dispatchEvent(new w.Event('error')); assert.equal(requests.length, 1, 'unrelated images are not avatar refresh targets');
dom.window.close();
`)
}
