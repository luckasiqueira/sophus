package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The Transcrever Mídia block offers OpenAI, Gemini and Groq credentials,
// describes what each reads and warns about Gemini's free-tier data use.
func TestFlowMediaTranscriptionCredentialsBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the media credentials regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var html strings.Builder
	if err := FlowBuilder().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=require('node:fs').readFileSync(0,'utf8');
(async()=>{
 const urls=['https://cdn.tailwindcss.com/','https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js','https://cdn.jsdelivr.net/npm/htmx-ext-sse@2.2.4'];const scripts={};
 await Promise.all(urls.map(async url=>{const response=await fetch(url,{signal:AbortSignal.timeout(20000)});assert.ok(response.ok);scripts[url]=await response.text();}));
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:900}});const errors=[];page.on('pageerror',error=>errors.push(error.message));
  const credentials=[{id:1,name:'OpenAI principal',provider:'openai',model:'gpt-4o-mini'},{id:2,name:'Gemini grátis',provider:'gemini',model:'gemini-2.5-flash'},
   {id:3,name:'Groq',provider:'groq',model:'openai/gpt-oss-20b'},{id:4,name:'DeepSeek',provider:'deepseek',model:'deepseek-flash'},{id:5,name:'Claude',provider:'claude',model:'claude-haiku-4-5-20251001'}];
  await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());
   if(url.hostname!=='ui.test'){const script=scripts[request.url()];return script?route.fulfill({contentType:'application/javascript',body:script}):route.fulfill({status:204});}
   if(url.pathname==='/flows/')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/flows/api/ai-credentials')return route.fulfill({json:{data:credentials}});
   if(url.pathname==='/flows/api/assignment-options')return route.fulfill({json:{data:{agents:[],departments:[]}}});
   if(url.pathname.startsWith('/flows/api/'))return route.fulfill({json:{data:[]}});
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/flows/');await page.waitForFunction(()=>state.aiCredentials.length===5);
  await page.locator('#btn-new-flow').click();await page.locator('#new-flow-blank').click();
  await page.locator('[data-add="mediaTranscription"]').click();
  await page.waitForFunction(()=>!document.getElementById('inspector').classList.contains('hidden'));
  const inspector=page.locator('#inspector');
  const offered=await page.evaluate(()=>[...document.querySelectorAll('#inspector [data-dd-field="credentialId"] input[type=radio]')].map(input=>input.value).filter(Boolean));
  assert.deepEqual(offered.sort(),['1','2','3','5'],'compatible credentials: '+JSON.stringify(offered));
  const text=await inspector.innerText();
  for(const expected of ['OpenAI, Gemini, Claude ou Groq','Gemini:','até 14 MB','Claude:','imagem até 5 MB','Groq:','imagem até 3 MB'])assert.ok(text.includes(expected),'inspector lacks '+expected);
  assert.equal(await page.locator('[data-gemini-privacy]').count(),0,'privacy notice shown without Gemini');
  const pick=value=>page.evaluate(v=>{const radio=[...document.querySelectorAll('#inspector [data-dd-field="credentialId"] input[type=radio]')].find(input=>input.value===v);radio.checked=true;radio.dispatchEvent(new Event('change',{bubbles:true}));},value);
  await pick('2');
  assert.equal(await page.evaluate(()=>state.flowData.nodes.find(n=>n.type==='mediaTranscription').data.credentialId),2);
  assert.equal(await page.locator('[data-gemini-privacy]').count(),1,'Gemini free-tier notice missing');
  await pick('3');
  assert.equal(await page.locator('[data-gemini-privacy]').count(),0,'notice kept for Groq');
  assert.deepEqual(errors,[]);console.log('PASS: media transcription offers OpenAI, Gemini and Groq with capabilities and Gemini privacy notice');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	t.Log(string(output))
}
