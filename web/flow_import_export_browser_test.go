package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFlowBuilderOffersJSONImportExport(t *testing.T) {
	var html strings.Builder
	if err := FlowBuilder().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`id="btn-import-flow"`, `id="flow-import-file"`, `accept=".json,application/json"`, `id="new-flow-import"`, `id="flow-export-modal"`, `id="flow-export-secrets"`, "data-export=", `id="btn-export-flow"`} {
		if !strings.Contains(html.String(), marker) {
			t.Errorf("flow builder missing %s", marker)
		}
	}
}

// Company A exports a flow; company B, where records have other ids and some
// do not exist, imports it. References are rebound by name, missing ones are
// cleared with a warning, media is re-uploaded and secrets stay out by default.
func TestFlowJSONExportImportAcrossCompaniesBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the flow import/export regression")
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
const assert=require('node:assert/strict'),fs=require('node:fs'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=fs.readFileSync(0,'utf8');
// Closing the browser after a download rejects Playwright's internal navigation
// promise; only that harness rejection is ignored.
process.on('unhandledRejection',error=>{if(error&&error.name==='TargetClosedError')return;console.error(error);process.exit(1);});
const png=Buffer.from('89504e470d0a1a0a0000000d4948445200000001000000010806000000','hex');
const flowA={id:7,name:'Atendimento principal',description:'Fluxo de entrada',connectionId:1,triggerType:'keyword',triggerValue:'oi',priority:5,isActive:true,revision:4,flowData:{
 nodes:[{id:'n_start',type:'start',position:{x:80,y:160},data:{}},
  {id:'n_tpl',type:'sendTemplate',position:{x:300,y:160},data:{templateId:3}},
  {id:'n_sub',type:'subflow',position:{x:520,y:160},data:{flowId:9}},
  {id:'n_assign',type:'assign',position:{x:740,y:160},data:{assignType:'agent',assignId:4}},
  {id:'n_http',type:'httpRequest',position:{x:960,y:160},data:{method:'POST',url:'https://api.example.test/leads',headerMode:'fields',headerFields:[{key:'Authorization',value:'Bearer segredo'},{key:'X-Trace',value:'1'}],bodyMode:'none',bodyFields:[],responseMappings:[]}},
  {id:'n_cb',type:'callback',position:{x:1180,y:160},data:{eventName:'lead',url:'https://hooks.example.test',token:'tok-123'}},
  {id:'n_img',type:'sendImage',position:{x:1400,y:160},data:{imagePath:'a1.png',imageMimeType:'image/png',mediaPreviewUrl:'/medias/1/flows/a1.png?token=t',mediaName:'logo.png'}}],
 edges:[{id:'e1',source:'n_start',target:'n_tpl'},{id:'e2',source:'n_tpl',target:'n_sub'}],
 settings:{noResponse:{enabled:true,reminderMinutes:30,reminderMessage:'Oi?',targetType:'department',targetId:5}}}};
const companies={
 A:{flows:[flowA,{id:9,name:'Coleta de dados',flowData:{nodes:[],edges:[]}}],templates:[{id:3,name:'Boas-vindas'}],agents:[{id:4,name:'Ana'}],departments:[{id:5,name:'Suporte'}],connections:[{id:1,name:'WhatsApp vendas',status:'connected'}]},
 B:{flows:[{id:90,name:'Outro fluxo',flowData:{nodes:[],edges:[]}}],templates:[{id:30,name:'boas-vindas'}],agents:[{id:40,name:'Ana'}],departments:[],connections:[{id:2,name:'Outro número',status:'connected'}]},
};
(async()=>{
 const urls=['https://cdn.tailwindcss.com/','https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js','https://cdn.jsdelivr.net/npm/htmx-ext-sse@2.2.4'];const scripts={};
 await Promise.all(urls.map(async url=>{const response=await fetch(url,{signal:AbortSignal.timeout(20000)});assert.ok(response.ok);scripts[url]=await response.text();}));
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try{
  const open=async(company,record)=>{
   const page=await browser.newPage({viewport:{width:1440,height:900},acceptDownloads:true});const errors=[];const alerts=[];
   page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>{alerts.push(dialog.message());dialog.accept();});
   const data=companies[company];
   await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());
    if(url.hostname!=='ui.test'){const script=scripts[request.url()];return script?route.fulfill({contentType:'application/javascript',body:script}):route.fulfill({status:204});}
    if(url.pathname==='/flows/')return route.fulfill({contentType:'text/html',body:html});
    if(url.pathname==='/flows/api/'&&request.method()==='POST'){const payload=request.postDataJSON();record.saved.push(payload);return route.fulfill({status:201,json:{data:{...payload,id:500,revision:1}}});}
    if(url.pathname==='/flows/api/')return route.fulfill({json:{data:data.flows}});
    const flowMatch=url.pathname.match(new RegExp('^/flows/api/(\\d+)$'));
    if(flowMatch)return route.fulfill({json:{data:data.flows.find(flow=>flow.id===Number(flowMatch[1]))}});
    if(url.pathname==='/flows/api/connections')return route.fulfill({json:{data:data.connections}});
    if(url.pathname==='/flows/api/assignment-options')return route.fulfill({json:{data:{agents:data.agents,departments:data.departments}}});
    if(url.pathname==='/flows/api/templates')return route.fulfill({json:{data:data.templates}});
    if(url.pathname==='/flows/api/media'){record.uploads.push(request.postDataBuffer());return route.fulfill({json:{data:{path:'b2.png',mimeType:'image/png',url:'/medias/2/flows/b2.png?token=u',name:'logo.png'}}});}
    if(url.pathname==='/medias/1/flows/a1.png')return route.fulfill({contentType:'image/png',body:png});
    if(url.pathname.startsWith('/flows/api/'))return route.fulfill({json:{data:[]}});
    if(url.pathname.startsWith('/sse'))return route.fulfill({contentType:'text/event-stream',body:'event: ready\ndata: ok\n\n'});
    return route.fulfill({status:204});
   });
   await page.goto('http://ui.test/flows/');await page.waitForFunction(()=>state.connections.length===1&&document.querySelectorAll('[data-export]').length>0);
   return {page,errors,alerts};
  };
  const download=async(page,includeSecrets)=>{
   await page.locator('[data-export="7"]').click();
   await page.waitForFunction(()=>document.getElementById('flow-export-modal').open);
   assert.equal(await page.locator('#flow-export-secrets-field').isVisible(),true,'secrets were not detected');
   assert.ok((await page.locator('#flow-export-secrets-list').innerText()).includes('Authorization'));
   assert.ok(!(await page.locator('#flow-export-secrets-list').innerText()).includes('X-Trace'),'harmless header flagged as secret');
   if(includeSecrets)await page.locator('#flow-export-secrets').check();
   const [file]=await Promise.all([page.waitForEvent('download'),page.locator('#flow-export-submit').click()]);
   assert.equal(file.suggestedFilename(),'atendimento-principal.sophus-flow.json');
   await page.waitForFunction(()=>!document.getElementById('flow-export-modal').open);
   return JSON.parse(fs.readFileSync(await file.path(),'utf8'));
  };
  const a={saved:[],uploads:[]};const sessionA=await open('A',a);
  const exported=await download(sessionA.page,false);
  assert.equal(exported.format,'sophus.flow');assert.equal(exported.version,1);assert.equal(exported.secretsIncluded,false);
  assert.equal(exported.flow.name,'Atendimento principal');assert.equal(exported.flow.priority,5);assert.equal(exported.flow.isActive,undefined,'activation state leaked into the file');
  const exportedNode=id=>exported.flow.flowData.nodes.find(node=>node.id===id).data;
  assert.equal(exportedNode('n_http').headerFields[0].value,'','Authorization header exported without consent');assert.equal(exportedNode('n_http').headerFields[1].value,'1');
  assert.equal(exportedNode('n_cb').token,undefined,'callback token exported without consent');
  assert.equal(exportedNode('n_img').imagePath,undefined,'company storage path exported');
  assert.deepEqual(exported.references.templates,{3:{name:'Boas-vindas'}});assert.deepEqual(exported.references.flows,{9:{name:'Coleta de dados'}});
  assert.deepEqual(exported.references.agents,{4:{name:'Ana'}});assert.deepEqual(exported.references.departments,{5:{name:'Suporte'}});
  assert.deepEqual(exported.references.connection,{id:1,name:'WhatsApp vendas'});
  assert.equal(exported.media.length,1);assert.equal(exported.media[0].nodeId,'n_img');
  assert.deepEqual(Buffer.from(exported.media[0].data.split(',')[1],'base64'),png,'embedded media differs from the stored file');
  const withSecrets=await download(sessionA.page,true);
  assert.equal(withSecrets.secretsIncluded,true);assert.equal(withSecrets.flow.flowData.nodes.find(n=>n.id==='n_cb').data.token,'tok-123');
  assert.deepEqual(sessionA.errors,[]);

  const b={saved:[],uploads:[]};const sessionB=await open('B',b);const page=sessionB.page;
  const filePath=require('node:path').join(require('node:os').tmpdir(),'sophus-flow-import-test.json');fs.writeFileSync(filePath,JSON.stringify(exported));
  await page.locator('#flow-import-file').setInputFiles(filePath);
  await page.waitForFunction(()=>state.currentFlow&&state.currentFlow.id===null&&!document.getElementById('view-editor').classList.contains('hidden'));
  const imported=await page.evaluate(()=>({flow:state.currentFlow,data:state.flowData,guide:document.getElementById('flow-starter-guide').textContent}));
  const data=id=>imported.data.nodes.find(node=>node.id===id).data;
  assert.equal(imported.flow.isActive,false,'imported flow was activated');assert.equal(imported.flow.connectionId,null);assert.equal(imported.flow.name,'Atendimento principal');
  assert.equal(data('n_tpl').templateId,30,'template not rebound by name');assert.equal(data('n_assign').assignId,40,'agent not rebound by name');
  assert.equal(data('n_sub').flowId,undefined,'missing subflow kept a foreign id');
  assert.equal(imported.data.settings.noResponse.enabled,false,'no-response kept a missing department');
  assert.equal(data('n_img').imagePath,'b2.png');assert.equal(data('n_img').mediaPreviewUrl,'/medias/2/flows/b2.png?token=u');
  assert.equal(b.uploads.length,1);assert.ok(b.uploads[0].includes(png),'uploaded media differs from the exported file');
  for(const expected of ['Coleta de dados','Suporte','WhatsApp vendas','rascunho inativo'])assert.ok(imported.guide.includes(expected),'guide lacks '+expected+': '+imported.guide);
  assert.equal(await page.evaluate(()=>flowIsDirty()),true,'imported draft not marked unsaved');
  await page.locator('#btn-save').click();await page.waitForFunction(()=>state.currentFlow.id===500);
  assert.equal(b.saved.length,1);assert.equal(b.saved[0].isActive,false);assert.equal(b.saved[0].flowData.nodes.find(n=>n.id==='n_tpl').data.templateId,30);
  for(const [content,message] of [['{','JSON válido'],[JSON.stringify({nodes:[{id:'x',type:'teleport',data:{}}],edges:[]}),'teleport'],[JSON.stringify({format:'sophus.flow',version:9,flow:{}}),'versão mais nova']]){
   fs.writeFileSync(filePath,content);sessionB.alerts.length=0;await page.locator('#flow-import-file').setInputFiles(filePath);
   await page.waitForFunction(()=>true);await page.waitForTimeout(300);
   assert.ok(sessionB.alerts.some(alert=>alert.includes(message)),'no error for invalid file: '+message+' '+JSON.stringify(sessionB.alerts));
  }
  fs.unlinkSync(filePath);
  assert.deepEqual(sessionB.errors,[]);
  console.log('PASS: export without secrets, media embedding, cross-company import with rebinding, warnings and save');
 }catch(error){console.error(error);process.exit(1);}finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	t.Log(string(output))
}
