package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSaaSDashboardOpensOnMonitoring(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var html strings.Builder
		if err := SaaSDashboard(enabled).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		page := html.String()
		for _, marker := range []string{`data-saas-tab="monitor" aria-selected="true"`, `data-saas-tab="companies" aria-selected="false"`, `id="monitor-panel"`, `<section id="companies-panel" class="hidden">`, "/management/api/monitoring"} {
			if !strings.Contains(page, marker) {
				t.Errorf("stressEnabled=%v: dashboard missing %s", enabled, marker)
			}
		}
		if strings.Contains(page, "data-monitor-open-stress class=") != enabled {
			t.Errorf("stressEnabled=%v: link to the stress test shown=%v", enabled, !enabled)
		}
	}
}

func TestSaaSMonitoringHomeBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for super-admin browser regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var html strings.Builder
	if err := SaaSDashboard(true).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=require('node:fs').readFileSync(0,'utf8');
(async()=>{const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage({viewport:{width:1300,height:900}});const errors=[];const cursors=[];let simulationRunning=false;
page.on('pageerror',error=>errors.push(error.stack||error.message));
const sample=i=>({t:new Date(Date.now()-(30-i)*2000).toISOString(),cpu:12+i*0.5,systemCpu:35,rssMB:220,runtimeMB:150,heapMB:95,systemMemoryPercent:61,load1:1.25,goroutines:410,gcPauseMs:0.4,dbOpen:10,dbInUse:2+i%3,pgConnections:18,databaseMB:2300,agentsOnline:7,simulatedAgentsOnline:simulationRunning?20:0,sseConnections:9,instancesConnected:3,simulatedInstancesConnected:simulationRunning?5:0});
await page.route('**/*',async route=>{const url=new URL(route.request().url());
 if(url.hostname!=='ui.test')return route.fulfill({status:204});
 if(url.pathname==='/admin')return route.fulfill({contentType:'text/html',body:html.replace('</head>','<style>.hidden{display:none!important}.flex{display:flex}</style></head>')});
 if(url.pathname==='/management/api/monitoring'){const since=Number(url.searchParams.get('since'));cursors.push(since);
  return route.fulfill({json:{data:{host:{cpus:4,os:'linux/amd64',systemMemoryMB:8000,procSupported:true,startedAt:'2026-10-14T08:00:00Z'},samples:since?[sample(30)]:Array.from({length:30},(_,i)=>sample(i)),simulationRunning,
   companies:[{companyId:2,name:'Beta Comércio',agentsOnline:5,simulatedAgentsOnline:simulationRunning?20:0,instancesConnected:2,simulatedInstancesConnected:0},{companyId:1,name:'Alfa Atendimento',agentsOnline:2,simulatedAgentsOnline:0,instancesConnected:1,simulatedInstancesConnected:0}]}}});}
 if(url.pathname==='/management/api/overview')return route.fulfill({json:{data:{companies:[{id:1,name:'Alfa Atendimento',email:'alfa@example.test'}],plans:[],settings:{},addonPrices:{}}}});
 return route.fulfill({json:{data:[]}});
});
await page.goto('http://ui.test/admin');
await page.waitForFunction(()=>document.querySelectorAll('#monitor-tiles article').length===12&&document.querySelectorAll('#monitor-companies tr').length===2);
assert.equal(await page.locator('#monitor-panel').isVisible(),true,'monitoring is not the home tab');
assert.equal(await page.locator('#companies-panel').isVisible(),false);assert.equal(await page.locator('#header-primary-action').isVisible(),false);
assert.equal(await page.locator('[data-saas-tab="monitor"]').getAttribute('aria-selected'),'true');
const firstTile=await page.locator('#monitor-tiles article').first().innerText();assert.ok(firstTile.includes('Agentes online')&&firstTile.includes('7'),firstTile);
assert.ok((await page.locator('#monitor-tiles').innerText()).includes('2,2 GB'),'database size not shown in GB');
assert.ok((await page.locator('#monitor-host').innerText()).includes('4 CPUs'));
assert.equal(await page.locator('[data-chart^="monitor-"] path').count(),8);
assert.ok((await page.locator('#monitor-companies tr').first().innerText()).includes('Beta Comércio'));
assert.equal(await page.locator('#monitor-simulation').isVisible(),false);
simulationRunning=true;await page.waitForFunction(()=>!document.getElementById('monitor-simulation').classList.contains('hidden'),null,{timeout:6000});
await page.waitForFunction(()=>document.getElementById('monitor-companies').innerText.includes('+20 simulados'));
assert.ok(cursors.filter(Boolean).length>=1,'polling did not send the since cursor');
const chart=page.locator('[data-chart="monitor-agents"] svg');await chart.scrollIntoViewIfNeeded();const box=await chart.boundingBox();
await page.mouse.move(box.x+box.width-12,box.y+box.height*0.5);
await page.waitForFunction(()=>!document.querySelector('[data-chart="monitor-agents"] .sophus-chart-tip').classList.contains('hidden'));
assert.ok((await page.locator('[data-chart="monitor-agents"] .sophus-chart-tip').innerText()).includes('agentes'));
await page.locator('[data-saas-tab="companies"]').click();assert.equal(await page.locator('#monitor-panel').isVisible(),false);assert.equal(await page.locator('#header-primary-action').isVisible(),true);
await page.locator('[data-saas-tab="monitor"]').click();assert.equal(await page.locator('#monitor-panel').isVisible(),true);
await page.locator('[data-monitor-open-stress]').click();assert.equal(await page.locator('#stress-panel').isVisible(),true);
assert.deepEqual(errors,[]);console.log('PASS: monitoring home, activity by company, simulation banner and charts');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
