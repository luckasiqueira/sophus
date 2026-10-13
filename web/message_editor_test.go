package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"
	"sophus/web/components"

	"github.com/google/uuid"
)

func TestMessageEditorSaveFailureAndDraftPreservationBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for message editor tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	conversation := repo.Conversation{Id: 12, URL: uuid.MustParse("00000000-0000-0000-0000-000000000012"), CanEditMessages: true}
	message := repo.MessageData{Id: 1, MessageId: "ORIGINAL", ConversationId: 12, IsFromMe: true, MediaType: "text", Text: "*Ana*:\n\nOriginal", CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now().Add(-time.Minute)}
	var scope, original, editor, edited strings.Builder
	if err := ConversationScope().Render(context.Background(), &scope); err != nil {
		t.Fatal(err)
	}
	if err := components.MessageSent(message, 3, conversation.URL.String()).Render(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	if err := ConversationMessageEditor(conversation).Render(context.Background(), &editor); err != nil {
		t.Fatal(err)
	}
	message.Text = "*Ana*:\n\nAlterado"
	message.DisplayData = json.RawMessage(`{"edited":true}`)
	if err := components.MessageSent(message, 3, conversation.URL.String(), true).Render(context.Background(), &edited); err != nil {
		t.Fatal(err)
	}
	shell := `<!doctype html><html><head><meta charset="UTF-8"><script src="/htmx.js"></script><style>.hidden{display:none!important}#chatMessages{height:200px;overflow:auto}#conversation-message-editor{width:500px}textarea{display:block;width:400px}</style></head><body><div id="conversation-panel" data-conversation-id="12" data-conversation-url="` + conversation.URL.String() + `" data-can-edit-messages="true">` + scope.String() + `<div id="chatMessages"><div style="height:300px">Histórico</div>` + original.String() + `<div style="height:300px">Histórico</div></div><textarea id="draft">Rascunho preservado</textarea>` + editor.String() + `</div></body></html>`
	input, _ := json.Marshal(map[string]string{"shell": shell, "edited": edited.String()})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),fixtures=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const htmx=await(await fetch('https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js',{signal:AbortSignal.timeout(15000)})).text();const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage(),errors=[];let fail=true,calls=0;page.on('pageerror',error=>errors.push(error.message));
await page.route('**/*',route=>{const url=new URL(route.request().url());if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:url.searchParams.has('disabled')?fixtures.shell.replace('data-can-edit-messages="true"','data-can-edit-messages="false"'):fixtures.shell});if(url.pathname==='/htmx.js')return route.fulfill({contentType:'application/javascript',body:htmx});if(url.pathname.endsWith('/edit')){calls++;const body=route.request().postDataJSON();assert.equal(body.messageId,'ORIGINAL');assert.equal(body.expectedText,'*Ana*:\n\nOriginal');assert.equal(body.text,'*Ana*:\n\nAlterado');return route.fulfill({status:fail?502:200,json:fail?{error:'WhatsApp indisponível'}:{data:{messageId:'ORIGINAL',text:body.text,html:fixtures.edited}}});}return route.fulfill({status:204});});
await page.goto('http://ui.test/');await page.locator('[data-edit-message-id]').waitFor({state:'visible'});await page.locator('#chatMessages').evaluate(el=>el.scrollTop=100);
await page.locator('[data-edit-message-id]').click();await page.waitForFunction(()=>document.getElementById('conversation-message-editor').open);const scrollPosition=await page.locator('#chatMessages').evaluate(el=>el.scrollTop);assert.equal(await page.locator('#conversation-message-editor-text').inputValue(),'*Ana*:\n\nOriginal');
await page.locator('#conversation-message-editor-text').fill('*Ana*:\n\nAlterado');await page.locator('#conversation-message-editor-save').click();await page.waitForFunction(()=>document.getElementById('conversation-message-editor-error').textContent==='WhatsApp indisponível');
assert.ok((await page.locator('#message-ORIGINAL').innerText()).includes('Original'));assert.equal(await page.locator('#conversation-message-editor-text').inputValue(),'*Ana*:\n\nAlterado');assert.equal(await page.locator('#draft').inputValue(),'Rascunho preservado');assert.equal(await page.locator('#conversation-message-editor-save').isEnabled(),true);
fail=false;await page.locator('#conversation-message-editor-save').click();await page.waitForFunction(()=>!document.getElementById('conversation-message-editor').open);assert.equal(calls,2);assert.equal(await page.locator('[data-message-id="ORIGINAL"]').count(),1);assert.ok((await page.locator('#message-ORIGINAL').innerText()).includes('Alterado'));assert.equal(await page.locator('[data-message-edited]').count(),1);assert.equal(await page.locator('#draft').inputValue(),'Rascunho preservado');assert.equal(await page.locator('#chatMessages').evaluate(el=>el.scrollTop),scrollPosition);
await page.evaluate(()=>window.conversationAbort.abort());assert.equal(await page.evaluate(()=>window.conversationAbort.signal.aborted),true);
await page.goto('http://ui.test/?disabled');assert.equal(await page.locator('[data-edit-message-id]').isVisible(),false);assert.deepEqual(errors,[]);console.log('PASS: edit dialog, WhatsApp failure/retry, OOB update, signature/draft/scroll preservation and disabled policy');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(input))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("message editor regression: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
