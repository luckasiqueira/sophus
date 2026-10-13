package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func searchPreviewFixture(first, last int) []repo.MessageData {
	messages := make([]repo.MessageData, 0, last-first+1)
	for id := first; id <= last; id++ {
		date := time.Date(2026, 10, 1, 0, id, 0, 0, time.UTC)
		messages = append(messages, repo.MessageData{Id: id, MessageId: fmt.Sprintf("message-%d", id), Text: fmt.Sprintf("Mensagem %d", id), MediaType: "text", CreatedAt: date, UpdatedAt: date, Timezone: "UTC", IsFromMe: id%2 == 0})
	}
	return messages
}

func TestSearchPreviewHighlightsOnlyTargetWithoutChatActions(t *testing.T) {
	var html strings.Builder
	messages := searchPreviewFixture(75, 85)
	messages[5].Text = "<script>unsafe</script>"
	if err := ConversationSearchPreview(repo.Conversation{Contact: repo.Contact{Name: "Contato"}}, messages, 80).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	content := html.String()
	if strings.Count(content, "data-search-context-id=") != 11 || strings.Count(content, "data-search-context-selected=") != 1 {
		t.Fatal("preview does not render exactly one selected message and its ten neighbors")
	}
	for _, unexpected := range []string{"hx-post=", "data-message-id=", "message-status-", "message-reactions-", "<script>"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("preview contains interactive chat state or unsafe content: %s", unexpected)
		}
	}
	if !strings.Contains(content, "&lt;script&gt;") || !strings.Contains(content, "Resultado encontrado") {
		t.Fatal("target content or highlight label missing")
	}
}

func TestOldMessageSearchPreviewAndHistoryNavigationBrowser(t *testing.T) {
	playwright := os.Getenv("ZUBLY_TEST_PLAYWRIGHT")
	if playwright == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to playwright-core for the Chrome navigation regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	agent := repo.Agent{Id: 7, CompanyId: 3}
	conversation := repo.Conversation{URL: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Status: "open", AgentID: &agent.Id, Contact: repo.Contact{Id: 1, Name: "Contato"}}
	var panel strings.Builder
	if err := ConversationPanel(conversation, agent, searchPreviewFixture(101, 150), nil, repo.ConversationMessagePage{HasOlder: true, BeforeID: 101}).Render(context.Background(), &panel); err != nil {
		t.Fatal(err)
	}
	// Execute the actual scope, search, and pager scripts with their rendered DOM;
	// unrelated recording/flow features are outside this navigation regression.
	scriptPattern := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	var scripts strings.Builder
	for _, match := range scriptPattern.FindAllStringSubmatch(panel.String(), -1) {
		if strings.Contains(match[1], "window.conversationAbort = controller") || strings.Contains(match[1], "const trigger = document.getElementById('conversation-message-search-button')") || strings.Contains(match[1], "window.ensureConversationMessage = async") {
			scripts.WriteString(match[0])
		}
	}
	css := `<style>.hidden{display:none!important}.flex{display:flex}.flex-col{flex-direction:column}#chatMessages{height:360px;overflow-y:auto}#chatMessages [data-message-id]{min-height:48px}dialog{width:700px;max-height:85vh}dialog>div{display:flex;flex-direction:column;max-height:85vh}#conversation-search-preview-content{overflow:auto;max-height:450px}article{padding:16px;margin:12px;border:1px solid #ddd}[data-search-context-selected]{background:#fff2bb}button{padding:8px}</style>`
	css += `<style>@media(min-width:1024px){#conversation-header-actions{display:flex!important}}</style>`
	fixtures := map[string]string{"shell": "<!doctype html><html><head>" + css + "</head><body>" + scriptPattern.ReplaceAllString(panel.String(), "") + scripts.String() + "</body></html>"}
	for _, target := range []int{80, 12} {
		var output strings.Builder
		if err := ConversationSearchPreview(conversation, searchPreviewFixture(target-5, target+5), target).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		fixtures[fmt.Sprintf("context-%d", target)] = output.String()
	}
	for _, page := range []struct {
		key         string
		first, last int
	}{{"older", 51, 100}, {"oldest", 1, 50}} {
		var output strings.Builder
		if err := ConversationMessages(conversation, agent, searchPreviewFixture(page.first, page.last), nil).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		fixtures[page.key] = output.String()
	}
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'); const {chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const fixtures=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try {
  const page=await browser.newPage(); const errors=[]; let historyCalls=0, contextCalls=0, failHistory=false;
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:fixtures.shell});
   if(url.pathname.endsWith('/search-context')) { contextCalls++; const id=url.searchParams.get('id'); return route.fulfill({json:{html:fixtures['context-'+id]}}); }
   if(url.pathname.endsWith('/search'))return route.fulfill({json:{total:2,matches:[{id:80,messageId:'message-80'},{id:12,messageId:'message-12'}]}});
   if(url.pathname.endsWith('/history')) {
    historyCalls++;
    if(failHistory){failHistory=false;return route.fulfill({status:500,json:{error:'Falha temporária no histórico'}});}
    const older=url.searchParams.get('before')==='101';
    return route.fulfill({json:{html:fixtures[older?'older':'oldest'],before:older?51:1,hasOlder:older}});
   }
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/');
  await page.locator('#conversation-message-search-button').click(); await page.locator('#conversation-message-search-input').fill('mensagem antiga');
  await page.locator('[data-search-context-selected]').waitFor();
  assert.equal(await page.locator('[data-search-context-id]').count(),11);
  assert.equal(await page.locator('[data-search-context-selected]').getAttribute('data-search-context-id'),'80');
  assert.equal(historyCalls,0,'opening preview loaded all intermediate pages');
  assert.equal(await page.locator('#chatMessages [data-message-id="message-80"]').count(),0,'preview was inserted into main chat');
  failHistory=true;
  await page.locator('#conversation-search-preview-load').click();
  await page.waitForFunction(()=>document.getElementById('conversation-search-preview-status').textContent.includes('Falha temporária'));
  assert.equal(await page.locator('#conversation-search-preview-load').isEnabled(),true,'history retry is disabled');
  await page.locator('#conversation-search-preview-load').click();
  await page.waitForFunction(()=>!document.getElementById('conversation-search-preview').open);
  assert.equal(historyCalls,2,'did not stop at the target history block');
  await page.waitForTimeout(600);
  const visible=await page.locator('#chatMessages [data-message-id="message-80"]').evaluate(el=>{const a=el.getBoundingClientRect(),b=document.getElementById('chatMessages').getBoundingClientRect();return a.top>=b.top&&a.bottom<=b.bottom;});
  assert.equal(visible,true,'scroll preservation overrode navigation to the target');
  assert.equal(await page.locator('#chatMessages [data-message-id="message-80"]').evaluate(el=>el.classList.contains('ring-amber-400')),true);
  await page.locator('#conversation-message-search-count').click(); await page.locator('#conversation-search-preview-next').click();
  await page.waitForFunction(()=>document.querySelector('[data-search-context-selected]')?.dataset.searchContextId==='12');
  assert.equal(historyCalls,2,'next preview loaded intermediate pages automatically');
  assert.equal(await page.locator('[data-search-context-id]').count(),11);
  await page.locator('#conversation-search-preview-load').click();
  await page.waitForFunction(()=>!document.getElementById('conversation-search-preview').open);
  assert.equal(historyCalls,3); assert.equal(await page.locator('#conversation-history-more').count(),0);
  assert.equal(await page.locator('#chatMessages [data-message-id="message-12"]').count(),1);
  assert.equal(await page.locator('#conversation-message-search-count').innerText(),'2 de 2');
  await page.locator('#conversation-message-search-count').click();
  await page.locator('#conversation-search-preview').press('Escape');
  assert.equal(await page.locator('#conversation-search-preview').evaluate(el=>el.open),false);
  await page.locator('#conversation-message-search-count').click(); await page.evaluate(()=>window.conversationAbort.abort());
  assert.equal(await page.locator('#conversation-search-preview').count(),0,'modal leaked after changing conversation');
  assert.deepEqual(errors,[]); console.log('PASS: bounded previews, highlighted target, history retry, sequential loading, stable scroll, result navigation, Escape and cleanup');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1});
`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("old-message navigation regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
