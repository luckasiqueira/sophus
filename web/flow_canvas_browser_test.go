package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Covers the canvas ergonomics: zoom confined to the flow, dragging a block
// without opening its properties, a floating properties panel, new blocks in
// the visible centre and a canvas that grows with its content.
func TestFlowCanvasZoomDragAndPlacementBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the flow canvas regression")
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
  const page=await browser.newPage({viewport:{width:1600,height:950}});const errors=[];
  page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());
  await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());
   if(url.hostname!=='ui.test'){const script=scripts[request.url()];return script?route.fulfill({contentType:'application/javascript',body:script}):route.fulfill({status:204});}
   if(url.pathname==='/flows/')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/flows/api/connections')return route.fulfill({json:{data:[{id:1,name:'WhatsApp',status:'connected'}]}});
   if(url.pathname==='/flows/api/assignment-options')return route.fulfill({json:{data:{agents:[],departments:[]}}});
   if(url.pathname.startsWith('/flows/api/'))return route.fulfill({json:{data:[]}});
   if(url.pathname.startsWith('/sse'))return route.fulfill({contentType:'text/event-stream',body:'event: ready\ndata: ok\n\n'});
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/flows/');await page.waitForFunction(()=>state.connections.length===1);
  await page.locator('#btn-new-flow').click();await page.locator('#new-flow-blank').click();
  await page.waitForFunction(()=>!document.getElementById('view-editor').classList.contains('hidden'));
  const wrapBox=await page.locator('#canvas-wrap').boundingBox();
  const canvasSize=await page.evaluate(()=>({w:parseFloat(canvas.style.width),h:parseFloat(canvas.style.height)}));
  assert.ok(canvasSize.w>=4000&&canvasSize.h>=3000,'canvas still small: '+JSON.stringify(canvasSize));
  assert.equal(await page.locator('#zoom-level').innerText(),'100%');

  // Adding blocks: centred in the visible area, clear of the panel and of each other.
  await page.locator('[data-add="sendText"]').click();
  await page.waitForFunction(()=>state.flowData.nodes.length===2);
  const inspectorBox=await page.locator('#inspector').boundingBox();
  assert.ok(inspectorBox.x>wrapBox.x+wrapBox.width*0.5,'properties panel does not float on the right');
  assert.ok(Math.abs(wrapBox.width-(await page.locator('#canvas-wrap').boundingBox()).width)<2,'properties panel shrank the canvas');
  const added=await page.locator('.node-card[data-node-id="'+await page.evaluate(()=>state.flowData.nodes[1].id)+'"]').boundingBox();
  assert.ok(added.x>wrapBox.x+150&&added.x+added.width<inspectorBox.x-10,'new block not in the free visible area: '+JSON.stringify(added));
  assert.ok(added.y>wrapBox.y+100&&added.y+added.height<wrapBox.y+wrapBox.height-100,'new block too close to the border');
  await page.locator('[data-add="sendText"]').click();await page.waitForFunction(()=>state.flowData.nodes.length===3);
  const noOverlap=async label=>{const boxes=[];for(const card of await page.locator('.node-card').all())boxes.push(await card.boundingBox());
   for(let i=0;i<boxes.length;i++)for(let j=i+1;j<boxes.length;j++){const a=boxes[i],b=boxes[j];
    assert.ok(a.x+a.width<=b.x||b.x+b.width<=a.x||a.y+a.height<=b.y||b.y+b.height<=a.y,label+': blocks '+i+' and '+j+' overlap');}};
  await noOverlap('blank flow');

  // Esc closes the panel; dragging a block moves it without opening properties.
  await page.keyboard.press('Escape');await page.waitForFunction(()=>state.selectedNodeId===null&&document.getElementById('inspector').classList.contains('hidden'));
  const startCard=page.locator('.node-card').first();let box=await startCard.boundingBox();
  const before=await page.evaluate(()=>({...state.flowData.nodes[0].position}));
  await page.mouse.move(box.x+110,box.y+40);await page.mouse.down();await page.mouse.move(box.x+170,box.y+70,{steps:6});await page.mouse.move(box.x+210,box.y+90,{steps:6});await page.mouse.up();
  let after=await page.evaluate(()=>({...state.flowData.nodes[0].position}));
  assert.deepEqual([after.x-before.x,after.y-before.y],[100,50],'drag did not follow the pointer at 100%');
  await page.waitForTimeout(100);
  assert.equal(await page.evaluate(()=>state.selectedNodeId),null,'dragging opened the properties panel');
  box=await startCard.boundingBox();await page.mouse.click(box.x+110,box.y+40);
  await page.waitForFunction(()=>state.selectedNodeId===state.flowData.nodes[0].id&&!document.getElementById('inspector').classList.contains('hidden'));
  await page.keyboard.press('Escape');

  // Ctrl + wheel zooms the flow only, anchored under the pointer.
  const pageZoom=await page.evaluate(()=>window.visualViewport.scale);
  const anchor={x:wrapBox.x+400,y:wrapBox.y+300};
  const anchorBefore=await page.evaluate(([x,y])=>canvasPoint(x,y),[anchor.x,anchor.y]);
  await page.mouse.move(anchor.x,anchor.y);await page.keyboard.down('Control');await page.mouse.wheel(0,400);await page.keyboard.up('Control');
  await page.waitForFunction(()=>state.zoom<0.9);
  const anchorAfter=await page.evaluate(([x,y])=>canvasPoint(x,y),[anchor.x,anchor.y]);
  assert.ok(Math.abs(anchorBefore.x-anchorAfter.x)<3&&Math.abs(anchorBefore.y-anchorAfter.y)<3,'zoom did not keep the point under the cursor');
  assert.equal(await page.evaluate(()=>window.visualViewport.scale),pageZoom,'the whole page zoomed');
  assert.equal(await page.locator('#zoom-level').innerText(),Math.round(await page.evaluate(()=>state.zoom)*100)+'%');
  assert.ok((await page.evaluate(()=>canvas.style.transform)).startsWith('scale(0.'));

  // At reduced zoom a screen drag maps to a larger logical move.
  await page.evaluate(()=>setZoom(0.5));const zoomed=page.locator('.node-card').first();await zoomed.scrollIntoViewIfNeeded();box=await zoomed.boundingBox();
  const zBefore=await page.evaluate(()=>({...state.flowData.nodes[0].position}));
  await page.mouse.move(box.x+40,box.y+15);await page.mouse.down();await page.mouse.move(box.x+90,box.y+15,{steps:8});await page.mouse.up();
  const zAfter=await page.evaluate(()=>({...state.flowData.nodes[0].position}));
  assert.equal(zAfter.x-zBefore.x,100,'drag ignores zoom');

  // Connecting blocks still works while zoomed.
  const ids=await page.evaluate(()=>state.flowData.nodes.map(n=>n.id));
  const handle=page.locator('.node-card[data-node-id="'+ids[0]+'"] .handle').last();const target=page.locator('.node-card[data-node-id="'+ids[1]+'"]');
  await target.scrollIntoViewIfNeeded();const h=await handle.boundingBox(),tb=await target.boundingBox();
  await page.mouse.move(h.x+h.width/2,h.y+h.height/2);await page.mouse.down();await page.mouse.move(tb.x+tb.width/2,tb.y+tb.height/2,{steps:10});await page.mouse.up();
  await page.waitForFunction(id=>state.flowData.edges.some(e=>e.target===id),ids[1]);

  // Keyboard zoom, panning the background and fit-to-screen.
  await page.locator('#canvas-wrap').click({position:{x:20,y:wrapBox.height-80}});
  await page.keyboard.press('Control+0');await page.waitForFunction(()=>state.zoom===1);
  const scrollBefore=await page.evaluate(()=>document.getElementById('canvas-wrap').scrollLeft);
  await page.mouse.move(wrapBox.x+wrapBox.width-200,wrapBox.y+wrapBox.height-60);await page.mouse.down();await page.mouse.move(wrapBox.x+wrapBox.width-500,wrapBox.y+wrapBox.height-60,{steps:8});await page.mouse.up();
  assert.ok(await page.evaluate(()=>document.getElementById('canvas-wrap').scrollLeft)>=scrollBefore+250,'dragging the background did not pan');
  await page.evaluate(()=>{state.flowData.nodes[2].position={x:6000,y:200};renderCanvas();});
  assert.ok(await page.evaluate(()=>parseFloat(canvas.style.width))>=6000+220+1000,'canvas did not grow past the last block');
  await page.locator('[data-zoom="fit"]').click();
  const fitted=await page.evaluate(()=>state.zoom);assert.ok(fitted<0.5,'fit kept 100% for a wide flow: '+fitted);
  for(const card of await page.locator('.node-card').all()){const b=await card.boundingBox();assert.ok(b.x>=wrapBox.x-1&&b.x+b.width<=wrapBox.x+wrapBox.width+1,'fit left a block outside the view');}

  // Collapsing the palette widens the canvas and is remembered.
  const panelWidth=(await page.locator('#palette-panel').boundingBox()).width;
  await page.locator('#palette-toggle').click();
  assert.ok((await page.locator('#palette-panel').boundingBox()).width<panelWidth*0.5,'palette did not collapse');
  assert.equal(await page.evaluate(()=>localStorage.getItem('sophus-flow-palette-collapsed')),'true');
  await page.locator('[data-add="delay"]').click();await page.waitForFunction(()=>state.flowData.nodes.some(n=>n.type==='delay'));

  // Starting from a model, whose blocks already fill the centre of the view.
  await page.evaluate(()=>{state.savedFingerprint=flowFingerprint();closeEditor();});
  await page.locator('#btn-new-flow').click();await page.locator('#new-flow-from-model').click();
  await page.locator('[name="flow-starter-choice"]').first().check();await page.locator('#new-flow-use-model').click();
  await page.waitForFunction(()=>state.currentFlow&&state.flowData.nodes.length>2);await page.waitForTimeout(200);
  for(const type of ['mediaTranscription','sendText','delay','sendImage'])await page.locator('[data-add="'+type+'"]').click();
  await noOverlap('model flow');
  assert.deepEqual(errors,[]);console.log('PASS: canvas zoom, drag without selection, floating panel, centred placement, growth and fit');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	t.Log(string(output))
}
