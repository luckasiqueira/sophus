package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAgentPreferencesBrowserSaveReloadAndAccountSwitch(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for preference browser tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	var html strings.Builder
	if err := AgentPreferencesPage().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),html=require('node:fs').readFileSync(0,'utf8');
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try {
  const page=await browser.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(()=>{window.EventSource=class extends EventTarget{close(){}};});
  const defaults={scrollAfterSend:true,scrollAfterSendMode:'always',messageSignatureEnabled:true,messageSignatureTemplate:'{agentName}',notifications:{toast:true,sound:true,browser:true,preview:true,groups:true}};
  const accounts=[structuredClone(defaults),structuredClone(defaults)];let account=0,fail=false;
  await page.route('**/*',route=>{
   const request=route.request(),url=new URL(request.url());
   if(url.pathname==='/preferences')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/management/api/preferences'){
    if(request.method()==='PUT'){if(fail)return route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({error:'Falha simulada'})});accounts[account]=request.postDataJSON();}
    return route.fulfill({contentType:'application/json',body:JSON.stringify({data:accounts[account]})});
   }
   return route.fulfill({status:200,contentType:'application/javascript',body:''});
  });
  await page.goto('http://ui.test/preferences');await page.waitForFunction(()=>!document.getElementById('agent-preferences-fields').disabled);
  await page.locator('#message-scroll-after-send').uncheck({force:true});
  await page.locator('#agent-signature-enabled').uncheck({force:true});
  await page.locator('#agent-signature-template').fill('*{agentName}*');
  for(const setting of Object.keys(defaults.notifications))await page.locator('[data-notification-setting='+setting+']').uncheck({force:true});
  await page.locator('#agent-preferences-form').evaluate(form=>form.requestSubmit());
   await page.waitForFunction(()=>document.getElementById('app-toast-text').textContent==='Preferências salvas.'&&!document.getElementById('app-toast').classList.contains('hidden'));
  assert.equal(accounts[0].scrollAfterSend,false);assert.equal(accounts[0].messageSignatureEnabled,false);
  for(const value of Object.values(accounts[0].notifications))assert.equal(value,false);
  assert.equal(await page.evaluate(()=>window.shouldAutoScrollAfterSend()),false);
  await page.reload();await page.waitForFunction(()=>!document.getElementById('agent-preferences-fields').disabled);
  assert.equal(await page.locator('#agent-signature-template').inputValue(),'*{agentName}*');assert.equal(await page.locator('#message-scroll-after-send').isChecked(),false);
   await page.locator('#message-scroll-after-send').check({force:true});await page.getByRole('button',{name:'Comportamento da rolagem'}).click();await page.getByRole('option',{name:'Exceto em citações'}).click();
  await page.locator('#agent-preferences-form').evaluate(form=>form.requestSubmit());await page.waitForFunction(()=>window.getAutoScrollAfterSendMode()==='except_quotes');
  assert.equal(await page.evaluate(()=>window.shouldAutoScrollAfterSend(true)),false);assert.equal(await page.evaluate(()=>window.shouldAutoScrollAfterSend(false)),true);
  fail=true;await page.locator('#agent-signature-template').fill('Não salvar');await page.locator('#agent-preferences-form').evaluate(form=>form.requestSubmit());await page.waitForFunction(()=>document.getElementById('agent-preferences-error').textContent==='Falha simulada');assert.equal(accounts[0].messageSignatureTemplate,'*{agentName}*');
  account=1;fail=false;await page.reload();await page.waitForFunction(()=>!document.getElementById('agent-preferences-fields').disabled);
  assert.equal(await page.locator('#message-scroll-after-send').isChecked(),true);assert.equal(await page.locator('#agent-signature-enabled').isChecked(),true);assert.equal(await page.locator('[data-notification-setting=sound]').isChecked(),true);assert.equal(await page.locator('#agent-signature-template').inputValue(),'{agentName}');
  assert.deepEqual(errors,[]);console.log('PASS: save/reload, quote exception, notifications, failed save and account isolation');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(html.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("preference browser regression: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
