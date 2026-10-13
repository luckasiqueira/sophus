package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sophus/internal/flowengine"
)

func TestFlowStarterCreationAndAppLayoutBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the Chrome flow-creation regression")
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
 try {
  const page=await browser.newPage({viewport:{width:1440,height:900}});const errors=[];const saves=[];const stored=[];
  page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());
  await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());
   if(url.hostname!=='ui.test'){const script=scripts[request.url()];return script?route.fulfill({contentType:'application/javascript',body:script}):route.fulfill({status:204});}
   if(url.pathname==='/flows/')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/flows/api/'){
    if(request.method()==='POST'){const payload=request.postDataJSON();assert.equal(payload.isActive,false,'starter was activated automatically');assert.equal(payload.triggerType,'always','starter still requires a keyword');assert.equal(payload.triggerValue,'','starter retained a keyword');saves.push(payload);const flow={...payload,id:saves.length,revision:1};stored.push(flow);return route.fulfill({json:{data:flow}});}
    return route.fulfill({json:{data:stored}});
   }
   if(url.pathname==='/flows/api/connections')return route.fulfill({json:{data:[{id:1,name:'WhatsApp da empresa',status:'connected'}]}});
   if(url.pathname==='/flows/api/assignment-options')return route.fulfill({json:{data:{agents:[],departments:[]}}});
   if(url.pathname.startsWith('/flows/api/'))return route.fulfill({json:{data:[]}});
   if(url.pathname.startsWith('/sse'))return route.fulfill({contentType:'text/event-stream',body:'event: ready\ndata: ok\n\n'});
   return route.fulfill({status:204});
  });
  await page.goto('http://ui.test/flows/');await page.waitForFunction(()=>state.connections.length===1);
  await page.waitForFunction(()=>document.getElementById('flowbuilder-page').getBoundingClientRect().width<window.innerWidth-50);
  assert.equal(await page.locator('#app-navigation').count(),1);assert.equal(await page.locator('#app-navigation').isVisible(),true);
  assert.equal(await page.locator('#app-sidebar-toggle').getAttribute('aria-expanded'),'false');
  const narrow=await page.locator('#app-navigation').evaluate(el=>el.getBoundingClientRect().width);
  await page.locator('#app-sidebar-toggle').click();await page.waitForFunction(()=>document.getElementById('app-navigation').getBoundingClientRect().width>=239);
  assert.equal(await page.locator('#app-sidebar-toggle').getAttribute('aria-expanded'),'true');assert.equal(await page.locator('#app-menu-links a[href="/contacts"] span').isVisible(),true);
  assert.equal(await page.evaluate(()=>localStorage.getItem('sophus-sidebar-expanded')),'true');
  await page.reload();await page.waitForFunction(()=>state.connections.length===1);
  assert.equal(await page.locator('#app-sidebar-toggle').getAttribute('aria-expanded'),'true');
  assert.ok(await page.locator('#app-navigation').evaluate(el=>el.getBoundingClientRect().width)>narrow+100);
  await page.locator('#app-user-menu summary').click();const userMenuLeft=await page.locator('#app-user-menu > div').evaluate(el=>el.getBoundingClientRect().left);assert.ok(userMenuLeft>=260,'profile popup overlaps expanded navigation');await page.locator('#app-user-menu summary').click();
  await page.locator('#app-sidebar-toggle').click();await page.waitForFunction(()=>document.getElementById('app-navigation').getBoundingClientRect().width<=65);
  assert.equal(await page.locator('#app-menu-links a[href="/contacts"] span').isVisible(),false);assert.equal(await page.evaluate(()=>localStorage.getItem('sophus-sidebar-expanded')),'false');
  await page.locator('#btn-new-flow').click();assert.equal(await page.locator('#new-flow-modal').evaluate(el=>el.open),true);
  assert.equal(await page.locator('#new-flow-blank').isVisible(),true);assert.equal(await page.locator('#new-flow-from-model').isVisible(),true);
  await page.locator('#new-flow-modal').press('Escape');assert.equal(await page.locator('#new-flow-modal').evaluate(el=>el.open),false);
  async function openDropdown(role){
   const wrapper=page.locator('[data-dd-role="'+role+'"]');await wrapper.locator('[data-dd-trigger]').click();
   await page.waitForFunction(role=>document.querySelector('[data-dd-role="'+role+'"] [data-dd-panel]')?.matches(':popover-open'),role);
   const visible=await wrapper.locator('[data-dd-panel]').evaluate(panel=>{const item=panel.querySelector('label');const rect=item.getBoundingClientRect();const hit=document.elementFromPoint(rect.left+Math.min(20,rect.width/2),rect.top+rect.height/2);return !!hit&&panel.contains(hit)&&rect.top>=0&&rect.bottom<=innerHeight;});
   assert.ok(visible,'dropdown options clipped by toolbar, inspector or modal: '+role);return wrapper;
  }
  await page.locator('#btn-new-flow').click();await page.locator('#new-flow-blank').click();
  assert.equal(await page.locator('#nodes-layer .node-card').count(),1);assert.equal(saves.length,0,'opening a blank editor saved a flow');
  const tiles=await page.evaluate(()=>['start','aiAgent'].map(type=>{const rect=document.querySelector('[data-add="'+type+'"]').getBoundingClientRect();return {x:rect.x,y:rect.y,width:rect.width,height:rect.height};}));
  assert.ok(Math.abs(tiles[0].width-tiles[0].height)<2,'palette block is not square: '+JSON.stringify(tiles));assert.ok(Math.abs(tiles[0].y-tiles[1].y)<2&&tiles[1].x>tiles[0].x,'palette blocks are not side by side: '+JSON.stringify(tiles));
  let dropdown=await openDropdown('connection');await dropdown.locator('[data-dd-panel] label').filter({hasText:'WhatsApp da empresa'}).click();assert.equal(await page.evaluate(()=>state.currentFlow.connectionId),1);
  dropdown=await openDropdown('trigger-type');await dropdown.locator('[data-dd-panel] label').filter({hasText:'Sempre responde'}).click();assert.equal(await page.evaluate(()=>state.currentFlow.triggerType),'always');
  assert.ok((await page.locator('[data-dd-role="trigger-type"] [data-dd-trigger]').innerText()).includes('Sempre responde'),'selected trigger label was not updated');
  await openDropdown('connection');await page.locator('[data-dd-role="connection"] [data-dd-trigger]').press('Escape');assert.equal(await page.locator('[data-dd-panel]:popover-open').count(),0);
  await page.locator('[data-add="sendText"]').click();assert.equal(await page.locator('#nodes-layer .node-card').count(),2,'square palette tile did not add a block');
  await page.locator('[data-add="condition"]').click();dropdown=await openDropdown('inspector-conditionOperator');await dropdown.locator('[data-dd-panel] label').filter({hasText:/^CONTÉM$/}).click();assert.equal(await page.evaluate(()=>state.flowData.nodes.find(node=>node.id===state.selectedNodeId).data.conditionOperator),'contains');
  await page.locator('#btn-flow-settings').click();await page.locator('#no-response-enabled').check();dropdown=await openDropdown('no-response-target-type');await dropdown.locator('[data-dd-panel] label').filter({hasText:'Setor',exact:true}).click();await page.locator('#btn-cancel-no-response').click();
  await page.locator('#btn-close-editor').click();
  const ids=await page.evaluate(()=>FLOW_STARTERS.map(template=>template.id));assert.equal(ids.length,5);
  for(const id of ids){
   await page.locator('#btn-new-flow').click();await page.locator('#new-flow-from-model').click();
   await page.locator('input[name="flow-starter-choice"][value="'+id+'"]').check();await page.locator('#new-flow-use-model').click();
   assert.equal(await page.locator('#app-navigation').isVisible(),true,'sidebar disappeared in editor');
   assert.equal(await page.evaluate(()=>state.currentFlow.triggerType),'always');assert.equal(await page.locator('#inp-trigger-value').isVisible(),false);
   const graph=await page.evaluate(()=>({types:state.flowData.nodes.map(node=>node.type),ids:state.flowData.nodes.map(node=>node.id),edges:state.flowData.edges,dirty:flowIsDirty(),active:state.currentFlow.isActive}));
   assert.ok(graph.dirty,'unsaved model draft was marked as saved');assert.equal(graph.active,false);assert.equal(graph.types.filter(type=>type==='start').length,1);
   assert.ok(graph.edges.every(edge=>graph.ids.includes(edge.source)&&graph.ids.includes(edge.target)),'template cloning broke an edge');
   assert.equal(await page.locator('#flow-starter-guide').isVisible(),true);
   await page.locator('#inp-name').fill('Modelo editado '+id);await page.locator('#btn-save').click();
   await page.waitForFunction(()=>!!state.currentFlow.id&&!state.saving&&!flowIsDirty());
   assert.equal(await page.evaluate(()=>FLOW_STARTERS.find(template=>template.id===state.currentFlow.name.replace('Modelo editado ','')).name.startsWith('Modelo editado')),false,'draft edits mutated catalog');
   await page.locator('#btn-close-editor').click();
  }
  assert.equal(saves.length,5);
  await page.setViewportSize({width:390,height:844});await page.locator('#btn-new-flow').click();await page.locator('#new-flow-from-model').click();
  await page.locator('input[name="flow-starter-choice"][value="simple-menu"]').check();await page.locator('#new-flow-use-model').click();
  await page.waitForTimeout(100);
  const bounds=await page.evaluate(()=>{const a=document.getElementById('flowbuilder-page').getBoundingClientRect(),b=document.getElementById('canvas-wrap').getBoundingClientRect();return {left:a.left,right:a.right,bottom:a.bottom,canvasWidth:b.width,canvasHeight:b.height,width:innerWidth,height:innerHeight};});
  assert.ok(bounds.left>=0&&bounds.right<=bounds.width+1&&bounds.bottom<=bounds.height+1,'mobile editor escaped app shell');assert.ok(bounds.canvasWidth>200&&bounds.canvasHeight>80,'mobile canvas collapsed');
  dropdown=await openDropdown('trigger-type');await dropdown.locator('[data-dd-panel] label').filter({hasText:'Sempre responde'}).click();
  await page.locator('#app-menu-button').click();assert.equal(await page.locator('#app-menu-links a[href="/flows/"]').isVisible(),true);
  assert.equal(await page.locator('#app-sidebar-toggle').isVisible(),false,'desktop collapse control shown in mobile navigation');
  assert.deepEqual(errors,[]);await page.evaluate(()=>state.savedFingerprint=flowFingerprint());console.log(JSON.stringify({saves}));
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("flow starter browser regression: %v\n%s", err, output)
	}
	var result struct {
		Saves []struct {
			FlowData json.RawMessage `json:"flowData"`
		} `json:"saves"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &result); err != nil {
		t.Fatalf("invalid browser test output: %v\n%s", err, output)
	}
	for _, saved := range result.Saves {
		if err := flowengine.ValidateActiveFlowData(saved.FlowData); err != nil {
			t.Fatalf("editor saved an invalid template graph: %v", err)
		}
	}
	t.Log("PASS: blank/model choices, all five model saves, graph validation, independent drafts and desktop/mobile app layout")
}
