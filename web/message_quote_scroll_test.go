package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"sophus/internal/repo"
	"sophus/web/components"
)

func TestQuotedReplyScrollPreferenceBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for quote-scroll regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	agent := repo.Agent{Id: 7, CompanyId: 1}
	conversation := repo.Conversation{Id: 1, URL: uuid.MustParse("00000000-0000-0000-0000-000000000001"), AgentID: &agent.Id, Status: "open", Contact: repo.Contact{Id: 1, Name: "Contato"}}
	var panel, preferences strings.Builder
	if err := ConversationPanel(conversation, agent, searchPreviewFixture(1, 30), nil).Render(context.Background(), &panel); err != nil {
		t.Fatal(err)
	}
	if err := MessagePreferences().Render(context.Background(), &preferences); err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	var scripts strings.Builder
	for _, match := range pattern.FindAllStringSubmatch(panel.String(), -1) {
		if strings.Contains(match[1], "window.conversationAbort = controller") || strings.Contains(match[1], "const quotedInput = composer.querySelector") || strings.Contains(match[1], "const shouldAutoScrollAfterSend = quoted") {
			scripts.WriteString(match[0])
		}
	}
	fixtures := map[string]string{"shell": "<!doctype html><html><head><meta charset=\"UTF-8\"><script src=\"/htmx.js\"></script><style>.hidden{display:none!important}#chatMessages{height:300px;overflow:auto}#chatMessages [data-message-id]{min-height:70px}.flex{display:flex}</style>" + preferences.String() + "</head><body>" + pattern.ReplaceAllString(panel.String(), "") + scripts.String() + "</body></html>"}
	for _, name := range []string{"quoted", "normal"} {
		message := repo.MessageData{MessageId: "sent-" + name, Text: "Resposta", MediaType: "text", IsFromMe: true}
		if name == "quoted" {
			message.QuotedId = "1ns"
			message.QuotedText = "Original"
		}
		var output strings.Builder
		if err := components.MessageSent(message, 1, conversation.URL.String()).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		fixtures[name] = output.String()
	}
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),fixtures=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const htmx=await(await fetch('https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js',{signal:AbortSignal.timeout(15000)})).text();const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:fixtures.shell});if(url.pathname==='/htmx.js')return route.fulfill({contentType:'application/javascript',body:htmx});if(url.pathname==='/api/message/send'){const parameters=new URLSearchParams(route.request().postData());return route.fulfill({contentType:'text/html',body:parameters.get('quotedId')?fixtures.quoted:fixtures.normal});}return route.fulfill({status:204});});
await page.goto('http://ui.test/');await page.waitForTimeout(100);
await page.evaluate(()=>{window.setAutoScrollAfterSend(true);window.setAutoScrollAfterSendMode('except_quotes');});
async function chooseQuote(){await page.evaluate(()=>{const form=document.getElementById('message-composer');form.querySelector('[name=quotedId]').value='1ns';document.getElementById('chatMessages').scrollTop=150;});}
await chooseQuote();
await page.evaluate(()=>{const chat=document.getElementById('chatMessages'),element=document.createElement('div');element.dataset.messageId='incoming';element.textContent='Nova mensagem';chat.appendChild(element);document.body.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail:{target:chat}}));document.body.dispatchEvent(new CustomEvent('htmx:sseMessage',{detail:{type:'message',data:element.outerHTML}}));});
await page.waitForTimeout(100);assert.equal(await page.locator('#chatMessages').evaluate(el=>el.scrollTop),150,'incoming message scrolled while quoting');
await page.locator('#message-composer [name=message]').fill('Resposta citada');await page.locator('#message-composer').evaluate(form=>form.requestSubmit());await page.locator('[data-message-id="sent-quoted"]').waitFor();await page.waitForTimeout(100);
assert.equal(await page.locator('#message-composer [name=quotedId]').inputValue(),'','reply state was not reset');assert.equal(await page.locator('#chatMessages').evaluate(el=>el.scrollTop),150,'quoted reply scrolled after composer reset');
await page.evaluate(html=>{const chat=document.getElementById('chatMessages');document.body.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail:{target:chat}}));document.body.dispatchEvent(new CustomEvent('htmx:sseMessage',{detail:{type:'message',data:html}}));},fixtures.quoted);await page.waitForTimeout(100);assert.equal(await page.locator('#chatMessages').evaluate(el=>el.scrollTop),150,'SSE echo scrolled quoted reply');
await page.locator('#message-composer [name=message]').fill('Resposta normal');await page.locator('#message-composer').evaluate(form=>form.requestSubmit());await page.locator('[data-message-id="sent-normal"]').waitFor();await page.waitForTimeout(100);assert.ok(await page.locator('#chatMessages').evaluate(el=>el.scrollTop>=el.scrollHeight-el.clientHeight-2),'normal reply did not scroll');
await page.evaluate(()=>window.setAutoScrollAfterSendMode('always'));await chooseQuote();await page.locator('#message-composer [name=message]').fill('Citação com rolagem');await page.locator('#message-composer').evaluate(form=>form.requestSubmit());await page.waitForTimeout(150);assert.ok(await page.locator('#chatMessages').evaluate(el=>el.scrollTop>=el.scrollHeight-el.clientHeight-2),'always mode did not scroll quoted reply');
assert.deepEqual(errors,[]);console.log('PASS: quoted HTMX reply after reset, incoming messages while quoting, SSE echo, normal reply and always mode');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("quote scroll regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
