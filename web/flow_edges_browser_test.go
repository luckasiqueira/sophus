package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Connections must be easy to hit: a click near the line (not only on the
// 2px stroke) selects it, at any zoom, and hovering shows a remove button.
func TestFlowConnectionRemovalHitAreaBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the flow connection regression")
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
  await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());
   if(url.hostname!=='ui.test'){const script=scripts[request.url()];return script?route.fulfill({contentType:'application/javascript',body:script}):route.fulfill({status:204});}
   if(url.pathname==='/flows/')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/flows/api/assignment-options')return route.fulfill({json:{data:{agents:[],departments:[]}}});
   if(url.pathname.startsWith('/flows/api/'))return route.fulfill({json:{data:[]}});
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/flows/');await page.waitForFunction(()=>typeof NODE_TYPES==='object');
  await page.locator('#btn-new-flow').click();await page.locator('#new-flow-blank').click();
  await page.waitForFunction(()=>!document.getElementById('view-editor').classList.contains('hidden'));
  await page.evaluate(()=>{
   state.flowData.nodes=[{id:'a',type:'start',position:{x:0,y:0},data:{}},{id:'b',type:'sendText',position:{x:520,y:260},data:{message:'Oi'}},{id:'c',type:'end',position:{x:520,y:0},data:{}}];
   state.flowData.edges=[{id:'e1',source:'a',target:'b',sourceHandle:''},{id:'e2',source:'b',target:'c',sourceHandle:''}];
   renderCanvas();fitView();setZoom(1);fitView();
  });
  // A point on the curve, then a point 7 screen pixels beside it: only the
  // wide hit area can catch the second one.
  const nearLine=async edgeId=>page.evaluate(id=>{const path=document.querySelector('[data-edge-id="'+id+'"] .edge-line');const length=path.getTotalLength();const p=path.getPointAtLength(length*0.3);
   const m=path.getScreenCTM();return {x:m.a*p.x+m.c*p.y+m.e,y:m.b*p.x+m.d*p.y+m.f+7};},edgeId);
  const confirm=page.locator('#removal-confirmation');
  let point=await nearLine('e1');
  await page.mouse.move(point.x,point.y);
  await page.waitForFunction(()=>document.querySelector('[data-edge-id="e1"]').classList.contains('active'));
  assert.equal(await page.locator('[data-edge-id="e1"] .edge-line').getAttribute('stroke'),'#6123cc','hovered connection is not highlighted');
  await page.waitForFunction(()=>getComputedStyle(document.querySelector('[data-edge-id="e1"] .edge-remove')).opacity==='1',null,{timeout:2000});
  await page.mouse.click(point.x,point.y);await confirm.waitFor({state:'visible'});
  await confirm.locator('[data-removal-cancel]').last().click();await confirm.waitFor({state:'hidden'});
  assert.equal(await page.evaluate(()=>state.flowData.edges.length),2,'cancel removed the connection');
  // The midpoint button removes it.
  const button=await page.locator('[data-edge-id="e1"] .edge-remove circle').boundingBox();
  await page.mouse.move(point.x,point.y);await page.mouse.click(button.x+button.width/2,button.y+button.height/2);
  await confirm.waitFor({state:'visible'});await page.locator('#removal-confirmation-submit').click();
  await page.waitForFunction(()=>state.flowData.edges.length===1&&state.flowData.edges[0].id==='e2');
  // Still easy at 40% zoom, where the stroke itself is under one pixel wide.
  await page.evaluate(()=>{setZoom(0.4);fitView();setZoom(0.4);});
  point=await nearLine('e2');
  await page.mouse.move(point.x,point.y);await page.waitForFunction(()=>document.querySelector('[data-edge-id="e2"]').classList.contains('active'));
  await page.mouse.click(point.x,point.y);await confirm.waitFor({state:'visible'});await page.locator('#removal-confirmation-submit').click();
  await page.waitForFunction(()=>state.flowData.edges.length===0);
  // Moving away clears the highlight; the background still pans and blocks still drag.
  assert.equal(await page.evaluate(()=>state.hoveredEdgeId),null);
  assert.deepEqual(errors,[]);console.log('PASS: wide connection hit area, hover highlight, midpoint remove button and zoom independence');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	t.Log(string(output))
}
