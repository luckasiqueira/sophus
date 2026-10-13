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

func contactHistoryUIFixture() (repo.Conversation, repo.Agent, repo.ConversationMessagePage, []repo.MessageData) {
	agent := repo.Agent{Id: 7, CompanyId: 3}
	anchor := repo.Conversation{Id: 2, ConnectionID: 1, URL: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Status: "open", AgentID: &agent.Id, AgentName: "Operador atual", DepartmentName: "Suporte", CreatedAt: time.Date(2026, 10, 1, 1, 41, 0, 0, time.UTC), Contact: repo.Contact{Id: 1, Name: "Contato"}}
	old := repo.Conversation{Id: 1, Status: "closed", AgentName: "Operador anterior", DepartmentName: "Comercial", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	messages := searchPreviewFixture(1, 125)
	for index := range messages {
		messages[index].ConversationId = 1
		if messages[index].Id > 100 {
			messages[index].ConversationId = 2
		}
	}
	messages[124].MediaType = "image"
	messages[124].MediaRetryURL = "/messages/" + anchor.URL.String() + "/media/retry"
	page := repo.ConversationMessagePage{HistoryScope: "contact", Conversations: []repo.Conversation{old, anchor}, HasOlder: true, BeforeID: 76}
	return anchor, agent, page, messages
}

func TestContactHistorySeparatesAppointmentsAndKeepsHistoricalMessagesReadOnly(t *testing.T) {
	anchor, agent, page, messages := contactHistoryUIFixture()
	var output strings.Builder
	if err := ConversationMessages(anchor, agent, messages[75:], nil, page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, want := range []string{`data-history-conversation="1"`, `data-history-conversation="2"`, "Operador anterior", "Comercial", "Atendimento atual", "Atendimento anterior"} {
		if !strings.Contains(html, want) {
			t.Fatalf("history missing %q", want)
		}
	}
	if strings.Contains(html, `data-reply-message-id="message-80"`) || strings.Contains(html, `/messages//react`) {
		t.Fatal("historical appointment exposed current-conversation actions")
	}
	if !strings.Contains(html, `data-reply-message-id="message-110"`) {
		t.Fatal("current appointment lost reply action")
	}
	previous, current := messages[99], messages[100]
	previous.MediaType = "image"
	previous.MediaPath = "image.webp"
	current.MediaType = "image"
	current.MediaPath = "image.webp"
	if receivedGalleryMatches(previous, current) {
		t.Fatal("gallery crossed appointment boundary")
	}
}

func TestContactHistoryScopeSwitchBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for Chrome/HTMX scope switching regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	anchor, agent, page, messages := contactHistoryUIFixture()
	var panel strings.Builder
	if err := ConversationPanel(anchor, agent, messages[100:], nil).Render(context.Background(), &panel); err != nil {
		t.Fatal(err)
	}
	scriptPattern := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	var scripts strings.Builder
	for _, script := range scriptPattern.FindAllStringSubmatch(panel.String(), -1) {
		if strings.Contains(script[1], "window.conversationAbort = controller") || strings.Contains(script[1], "const trigger = document.getElementById('conversation-message-search-button')") || strings.Contains(script[1], "window.ensureConversationMessage = async") || strings.Contains(script[0], "data-history-scope=") || strings.Contains(script[1], "const button=event.target.closest('[data-media-retry]')") || strings.Contains(script[1], "const shouldAutoScrollAfterSend = quoted") {
			scripts.WriteString(script[0])
		}
	}
	css := `<style>.hidden{display:none!important}.flex{display:flex}.flex-col{flex-direction:column}.htmx-indicator{display:none}#chatMessages{height:360px;overflow:auto}#chatMessages [data-message-id]{min-height:48px}dialog{width:700px;max-height:85vh}dialog>div{display:flex;flex-direction:column;max-height:85vh}#conversation-search-preview-content{overflow:auto;max-height:450px}article{padding:16px;margin:12px}a,button{padding:8px}</style>`
	css += `<style>@media(min-width:1024px){#conversation-header-actions{display:flex!important}}</style>`
	fixtures := map[string]string{"shell": "<!doctype html><html><head>" + css + `<script src="/htmx.js"></script></head><body>` + scriptPattern.ReplaceAllString(panel.String(), "") + scripts.String() + "</body></html>"}
	for _, scope := range []string{"contact", "current"} {
		var output strings.Builder
		selected := page
		subset := messages[75:]
		if scope == "current" {
			selected = repo.ConversationMessagePage{}
			subset = messages[100:]
		}
		if err := ConversationTimelineContent(anchor, agent, subset, nil, selected).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		fixtures[scope] = output.String()
	}
	var older, preview strings.Builder
	if err := ConversationMessages(anchor, agent, messages[25:75], nil, page).Render(context.Background(), &older); err != nil {
		t.Fatal(err)
	}
	if err := ConversationSearchPreview(anchor, messages[44:55], 50, page).Render(context.Background(), &preview); err != nil {
		t.Fatal(err)
	}
	fixtures["older"] = older.String()
	fixtures["preview"] = preview.String()
	var recovered strings.Builder
	phone := messages[124]
	phone.IsFromMe = true
	phone.MediaType = "image"
	phone.MediaPath = "phone.jpg"
	if err := components.MessageSent(phone, agent.CompanyId, anchor.URL.String(), true).Render(context.Background(), &recovered); err != nil {
		t.Fatal(err)
	}
	fixtures["recovered"] = recovered.String()
	var edited strings.Builder
	edit := messages[123]
	edit.Text = "Texto atualizado pela edição"
	edit.DisplayData = json.RawMessage(`{"edited":true}`)
	if err := components.MessageReceived(edit, agent.CompanyId, anchor.Contact.Id, anchor.URL.String(), true).Render(context.Background(), &edited); err != nil {
		t.Fatal(err)
	}
	fixtures["edited"] = edited.String()
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const fixtures=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{
 const htmx=await(await fetch('https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js',{signal:AbortSignal.timeout(15000)})).text();
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try{const page=await browser.newPage();const errors=[];const calls=[];
 page.on('pageerror',error=>errors.push(error.message));
 await page.route('**/*',async route=>{const url=new URL(route.request().url());calls.push(url.pathname+url.search);
  if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:fixtures.shell});
  if(url.pathname==='/htmx.js')return route.fulfill({contentType:'application/javascript',body:htmx});
  if(url.pathname==='/media-recovery')return route.fulfill({contentType:'text/html',body:fixtures.recovered});
  if(url.pathname.endsWith('/media/retry'))return route.fulfill({status:409,json:{error:'O arquivo não está mais disponível; solicite o reenvio.'}});
  if(url.pathname.endsWith('/timeline'))return route.fulfill({contentType:'text/html',body:fixtures[url.searchParams.get('history')]});
  if(url.pathname.endsWith('/search'))return route.fulfill({json:{total:1,matches:[{id:50,conversationId:1,messageId:'message-50'}]}});
  if(url.pathname.endsWith('/search-context')){assert.equal(url.searchParams.get('history'),'contact');return route.fulfill({json:{html:fixtures.preview}});}
  if(url.pathname.endsWith('/history')){assert.equal(url.searchParams.get('history'),'contact');return route.fulfill({json:{html:fixtures.older,before:26,hasOlder:true}});}
  return route.fulfill({status:204});
 });
 await page.goto('http://ui.test/');await page.waitForFunction(()=>typeof htmx==='object');
 await page.locator('#message-composer [name="message"]').fill('Rascunho preservado');
 await page.evaluate(()=>{document.getElementById('message-composer').dataset.identity='composer-original';document.getElementById('chatMessages').dataset.identity='stream-original';});
 await page.locator('#message-message-125 [data-media-retry]').click();await page.waitForFunction(()=>document.querySelector('#message-message-125 [data-media-retry-error]')?.textContent.includes('solicite o reenvio'));
 assert.equal(await page.locator('#message-message-125 [data-media-retry]').isEnabled(),true,'media recovery cannot be retried after provider failure');
 await page.locator('[data-history-mode="contact"]').click();
 await page.waitForFunction(()=>document.getElementById('chatMessages').dataset.historyScope==='contact');
 assert.equal(await page.locator('#message-composer [name="message"]').inputValue(),'Rascunho preservado');
 assert.equal(await page.locator('#message-composer').getAttribute('data-identity'),'composer-original');
 assert.equal(await page.locator('#chatMessages').getAttribute('data-identity'),'stream-original');
 assert.equal(await page.locator('#message-composer [name="conversation"]').inputValue(),'00000000-0000-0000-0000-000000000002');
 assert.equal(await page.locator('[data-history-conversation="1"]').count(),1);assert.equal(await page.locator('[data-history-conversation="2"]').count(),1);
 await page.locator('#conversation-message-search-button').click();await page.locator('#conversation-message-search-input').fill('mensagem anterior');
 await page.locator('[data-search-context-selected]').waitFor();
 assert.equal(await page.locator('#conversation-search-preview [data-history-conversation="1"]').count(),1);
 await page.locator('#conversation-search-preview-load').click();
 await page.waitForFunction(()=>!document.getElementById('conversation-search-preview').open);
 assert.equal(await page.locator('#chatMessages [data-message-id="message-50"]').count(),1);
 assert.equal(await page.locator('#chatMessages [data-history-conversation="1"]').count(),1,'appointment separator duplicated at page boundary');
 assert.ok(calls.some(url=>url.includes('/search?')&&url.includes('history=contact')));
 await page.locator('#conversation-message-search-close').click();await page.locator('[data-history-mode="current"]').click();
 await page.waitForFunction(()=>document.getElementById('chatMessages').dataset.historyScope==='current');
 assert.equal(await page.locator('#chatMessages [data-message-id="message-50"]').count(),0);
 assert.equal(await page.locator('#message-composer [name="message"]').inputValue(),'Rascunho preservado');
 assert.equal(await page.evaluate(()=>window.conversationAbort.signal.aborted),false);
 assert.ok((await page.locator('#chatMessages').getAttribute('sse-swap')).includes('message-update'));
 await page.evaluate(()=>htmx.ajax('GET','/media-recovery',{target:'#chatMessages',swap:'beforeend'}));
 assert.equal(await page.locator('#chatMessages [data-message-id="message-125"]').count(),1,'recovered phone image duplicated the original message');
 assert.equal(await page.locator('#message-message-125 img[src$="phone.jpg/content"]').count(),1,'recovered attachment was not displayed');
 await page.waitForFunction(()=>document.getElementById('message-message-125')?.classList.contains('justify-end'));
  assert.equal(await page.locator('#message-message-125').evaluate(el=>el.classList.contains('justify-end')),true,'phone image displayed as incoming');
  const originalCount=await page.locator('#chatMessages [data-message-id]').count();await page.locator('#chatMessages').evaluate(el=>el.scrollTop=100);
  await page.evaluate(html=>{htmx.swap('#chatMessages',html,{swapStyle:'beforeend'});document.body.dispatchEvent(new CustomEvent('htmx:sseMessage',{detail:{type:'message-update',data:html}}));},fixtures.edited);await page.waitForTimeout(100);
  assert.equal(await page.locator('#chatMessages [data-message-id="message-124"]').count(),1,'edit duplicated original message');assert.equal(await page.locator('#chatMessages [data-message-id]').count(),originalCount);
  assert.ok((await page.locator('#message-message-124').innerText()).includes('Texto atualizado pela edição'));assert.equal(await page.locator('#message-message-124 [data-message-edited]').count(),1);
  assert.equal(await page.locator('#chatMessages').evaluate(el=>el.scrollTop),100,'edit moved scroll position');assert.equal(await page.locator('#message-composer [name="message"]').inputValue(),'Rascunho preservado');
  assert.deepEqual(errors,[]);console.log('PASS: HTMX scope switches, preserved draft/composer/SSE, history search/pagination, media recovery and edit replacement without scroll jump');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("contact history browser regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
