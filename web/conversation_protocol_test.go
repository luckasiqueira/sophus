package web

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestConversationDisplaysCompanyProtocolInsteadOfDatabaseID(t *testing.T) {
	owner := 7
	conversation := repo.Conversation{Id: 98765, Protocol: 640, URL: uuid.New(), ConnectionID: 1, AgentID: &owner, Status: repo.ConversationStatusOpen, Contact: repo.Contact{Id: 1, Name: "Cliente"}}
	var html strings.Builder
	if err := MessageSingle(conversation, repo.Agent{Id: owner}, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`aria-label="Protocolo 640"`, `text-gray-400`, ">#640</span>", `id="conversation-protocol-search"`, `hx-get="/messages/protocol"`, `name="protocol"`} {
		if !strings.Contains(html.String(), expected) {
			t.Fatalf("missing protocol UI %q", expected)
		}
	}
	if strings.Contains(html.String(), "#98765") {
		t.Fatal("global database ID displayed as protocol")
	}
	var divider strings.Builder
	if err := ConversationAppointmentDivider(conversation, conversation, "UTC").Render(context.Background(), &divider); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(divider.String(), "#640") || strings.Contains(divider.String(), "#98765") {
		t.Fatalf("incorrect history protocol: %s", divider.String())
	}
}

func TestProtocolSearchNavigatesByUUIDBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for protocol navigation checks")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := Messages(nil, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	markup := pattern.ReplaceAllString(output.String(), "")
	markup = strings.Replace(markup, "</head>", `<style>.hidden{display:none!important}</style><script src="/htmx.js"></script></head>`, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),html=require('node:fs').readFileSync(0,'utf8');
(async()=>{
 const htmx=await(await fetch('https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js',{signal:AbortSignal.timeout(15000)})).text();
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try{
  const page=await browser.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(()=>{window.showAppNotice=(title,message)=>{window.protocolNotice={title,message};};});
  const uuid='00000000-0000-0000-0000-000000000999';
  await page.route('**/*',route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/messages')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/htmx.js')return route.fulfill({contentType:'application/javascript',body:htmx});
   if(url.pathname==='/messages/protocol'){
    if(url.searchParams.get('protocol')==='#1002')return route.fulfill({status:204,headers:{'HX-Location':JSON.stringify({path:'/messages/'+uuid,target:'#conversation-panel',swap:'outerHTML'})}});
    return route.fulfill({status:404,contentType:'application/json',body:JSON.stringify({error:'Protocolo não encontrado nesta empresa.'})});
   }
   if(url.pathname==='/messages/'+uuid)return route.fulfill({contentType:'text/html',body:'<div id="conversation-panel"><span id="protocol-found">#1002</span></div>'});
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/messages');
  await page.locator('#conversation-protocol-input').fill('#1002');await page.getByRole('button',{name:'Pesquisar protocolo',exact:true}).click();
  await page.locator('#protocol-found').waitFor();assert.equal(await page.locator('#protocol-found').textContent(),'#1002');
  assert.equal(new URL(page.url()).pathname,'/messages/'+uuid,'navigation must keep the globally unique UUID, not the local protocol');
  await page.locator('#conversation-protocol-input').fill('640');await page.getByRole('button',{name:'Pesquisar protocolo',exact:true}).click();
  await page.waitForFunction(()=>Boolean(window.protocolNotice));assert.ok((await page.evaluate(()=>window.protocolNotice.message)).includes('nesta empresa'));
  assert.equal(new URL(page.url()).pathname,'/messages/'+uuid,'failed lookup must preserve the current conversation');assert.deepEqual(errors,[]);
  console.log('PASS: protocol form, UUID navigation, history URL and inline failed-search notice');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
`)
	command.Stdin = strings.NewReader(markup)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("protocol browser check: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
