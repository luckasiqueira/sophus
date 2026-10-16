package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sophus/internal/flowengine"
)

// The editor and the server keep separate lists of each block's exits. This
// test reads the editor's exits for every block type in a real browser and
// checks that the server accepts each one (and still rejects unknown exits),
// so a block whose connections cannot be saved fails here instead of in use.
// It also checks that multi-exit blocks name their exits on the canvas.
func TestFlowBuilderExitsMatchServerValidationBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT to run the flow exits regression")
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
  const page=await browser.newPage({viewport:{width:1600,height:950}});const errors=[];page.on('pageerror',error=>errors.push(error.message));
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
  const exits=await page.evaluate(()=>Object.keys(NODE_TYPES).map(type=>{const node={id:'n',type,data:defaultDataFor(type)};return {type,data:node.data,handles:outputsFor(node).map(o=>o.handle),labels:outputsFor(node).map(o=>o.label)};}));
  // Multi-exit blocks name every exit, and the names do not cover the summary text.
  for(const block of exits.filter(item=>item.handles.length>1&&!['sendMenu','sendButtons'].includes(item.type))){
   await page.evaluate(type=>{state.flowData.nodes=[{id:'probe',type,position:{x:300,y:300},data:defaultDataFor(type)}];state.flowData.edges=[];state.selectedNodeId=null;renderCanvas();},block.type);
   const card=page.locator('.node-card[data-node-id="probe"]');
   const labels=await card.locator('.handle-label').allInnerTexts();
   assert.deepEqual(labels,block.labels,block.type+': exits are not named on the canvas');
   const body=await card.locator(':scope > div').nth(1).evaluate(el=>{const r=el.getBoundingClientRect(),pad=parseFloat(getComputedStyle(el).paddingRight);return {right:r.right-pad};});
   for(const label of await card.locator('.handle-label').all()){const box=await label.boundingBox();assert.ok(box.x>=body.right-1,block.type+': exit label overlaps the block text');}
  }
  assert.deepEqual(errors,[]);
  console.log('RESULT:'+JSON.stringify(exits));
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	marker := strings.LastIndex(string(output), "RESULT:")
	if marker < 0 {
		t.Fatalf("browser did not report the editor exits:\n%s", output)
	}
	var blocks []struct {
		Type    string                 `json:"type"`
		Data    map[string]interface{} `json:"data"`
		Handles []string               `json:"handles"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output)[marker+len("RESULT:"):])), &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) < 30 {
		t.Fatalf("only %d block types reported", len(blocks))
	}
	// Edges are validated before block configuration, so an exit problem is
	// reported even when the default block data is not yet savable.
	validate := func(block string, data map[string]interface{}, handle string) error {
		flow := map[string]interface{}{
			"nodes": []map[string]interface{}{{"id": "n", "type": block, "data": data}, {"id": "end", "type": "end", "data": map[string]interface{}{}}},
			"edges": []map[string]interface{}{{"id": "e", "source": "n", "target": "end", "sourceHandle": handle}},
		}
		raw, _ := json.Marshal(flow)
		return flowengine.ValidateFlowData(raw)
	}
	for _, block := range blocks {
		for _, handle := range block.Handles {
			if err := validate(block.Type, block.Data, handle); err != nil && strings.Contains(err.Error(), "saída inválida") {
				t.Errorf("block %s: the editor offers exit %q but the server rejects it: %v", block.Type, handle, err)
			}
		}
		if len(block.Handles) > 0 {
			err := validate(block.Type, block.Data, "saida-inexistente")
			if err == nil || !strings.Contains(err.Error(), "saída inválida") {
				t.Errorf("block %s: the server accepted an exit the editor never offers (%v)", block.Type, err)
			}
		}
	}
	t.Log(fmt.Sprintf("checked exits of %d block types", len(blocks)))
}
