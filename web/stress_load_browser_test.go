package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSaaSStressPanelFollowsEnvironmentFlag(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var html strings.Builder
		if err := SaaSDashboard(enabled).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{`data-saas-tab="stress"`, `id="stress-panel"`, "/management/api/stress-test"} {
			if strings.Contains(html.String(), marker) != enabled {
				t.Errorf("enabled=%v: presence of %s = %v", enabled, marker, !enabled)
			}
		}
	}
}

func TestSaaSStressPanelGeneratesSimulatesAndRevertsBrowser(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=require('node:fs').readFileSync(0,'utf8');
(async()=>{const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage({viewport:{width:1300,height:900}});const errors=[];const posts=[];const deleted=[];let job=null,simulation=null,tick=0;
page.on('pageerror',error=>errors.push(error.stack||error.message));page.on('dialog',dialog=>dialog.accept());
const contacts=[{contactId:44,companyId:2,name:'Contato fictício · Estresse 0042',protocol:7,agentName:'Bruna',messageCount:2500,createdAt:'2026-10-14T12:00:00Z'}];
const sample=i=>({t:new Date(Date.now()-(20-i)*2000).toISOString(),cpu:10+i,systemCpu:30,rssMB:200+i,runtimeMB:150,heapMB:90,systemMemoryPercent:55,goroutines:300+i,dbOpen:12,dbInUse:3,pgConnections:20,requestsPerSecond:40+i,avgLatencyMs:20,p95LatencyMs:80+i,errorsPerSecond:0,webhooksPerSecond:5,simulatorSseOpen:40,simulatedAgentsOnline:20});
await page.route('**/*',async route=>{const request=route.request(),url=new URL(request.url()),path=url.pathname;
 if(url.hostname!=='ui.test')return route.fulfill({status:204});
 if(path==='/admin')return route.fulfill({contentType:'text/html',body:html.replace('</head>','<style>.hidden{display:none!important}.flex{display:flex}</style></head>')});
 if(path==='/management/api/overview')return route.fulfill({json:{data:{companies:[{id:1,name:'Alfa Atendimento',email:'alfa@example.test'},{id:2,name:'Beta Comércio',email:'beta@example.test'}],plans:[],settings:{},addonPrices:{}}}});
 if(path==='/management/api/stress-test/companies/2/agents')return route.fulfill({json:{data:[{id:9,name:'Bruna',email:'bruna@example.test',role:'agent',isActive:true},{id:50,name:'Agente simulado 001',email:'simulado-x@stress.invalid',role:'agent',isActive:true}]}});
 if(path==='/management/api/stress-test/companies/2/contacts')return route.fulfill({json:{data:contacts}});
 if(/\/companies\/(0|2)\/summary$/.test(path))return route.fulfill({json:{data:{contacts:1,simulatedContacts:25,agents:20,connections:5,conversations:26,messages:3100}}});
 if(path==='/management/api/stress-test/metrics'){tick++;
  if(job&&job.status==='running'){job={...job,done:Math.min(job.total,job.done+10000)};if(job.done>=job.total)job={...job,status:'completed',finishedAt:new Date().toISOString()};}
  const since=Number(url.searchParams.get('since'));const samples=since?[sample(20+tick)]:Array.from({length:20},(_,i)=>sample(i));
  return route.fulfill({json:{data:{host:{cpus:8,gomaxprocs:8,os:'linux/amd64',systemMemoryMB:16000,procSupported:true},samples,simulation,job}}});}
 if(request.method()==='POST'){posts.push([path,request.postDataJSON()]);
  if(path.endsWith('/messages')){job={kind:'generate',status:'running',companyId:2,agentName:'Bruna',contactName:'Contato fictício · Estresse 0099',protocol:8,total:20000,done:0,startedAt:new Date().toISOString()};return route.fulfill({status:202,json:{data:job}});}
  if(path.endsWith('/simulation')){const config=request.postDataJSON();simulation={running:true,config,startedAt:new Date().toISOString(),endsAt:new Date(Date.now()+600000).toISOString(),agentsOnline:20,instancesOnline:5,requests:1200,errors:0,webhooks:300,webhookErrors:0};return route.fulfill({status:202,json:{data:simulation}});}
  if(path.endsWith('/simulation/stop')){simulation={...simulation,running:false,finishedAt:new Date().toISOString(),stopReason:'interrompida pelo administrador'};return route.fulfill({json:{data:simulation}});}
  if(path.endsWith('/revert')){job={kind:'revert',status:'running',companyId:request.postDataJSON().companyId,contactName:'26 contatos, 20 agentes, 5 instâncias',total:3100,done:0,startedAt:new Date().toISOString()};return route.fulfill({status:202,json:{data:job}});}
 }
 if(request.method()==='DELETE'){deleted.push(path);job={kind:'cleanup',status:'completed',companyId:2,contactName:contacts[0].name,total:2500,done:2500,startedAt:new Date().toISOString(),finishedAt:new Date().toISOString()};return route.fulfill({status:202,json:{data:job}});}
 return route.fulfill({json:{data:[]}});
});
await page.goto('http://ui.test/admin');await page.waitForFunction(()=>saasState.companies.length===2);
await page.locator('[data-saas-tab="stress"]').click();
assert.equal(await page.locator('#stress-panel').isVisible(),true);assert.equal(await page.locator('#monitor-panel').isVisible(),false);assert.equal(await page.locator('#header-primary-action').isVisible(),false);
await page.locator('#stress-company-filter').fill('beta');
await page.waitForFunction(()=>document.querySelectorAll('#stress-agent option').length===2);
assert.ok(!(await page.locator('#stress-agent').innerText()).includes('simulado'),'simulated agents offered as conversation owners');
await page.waitForFunction(()=>document.querySelectorAll('#stress-contacts tr').length===1&&document.querySelectorAll('#stress-summary > div').length===6);
assert.ok((await page.locator('#stress-summary').innerText()).includes('3.100'));
await page.waitForFunction(()=>document.querySelectorAll('#stress-tiles article').length===6&&document.querySelectorAll('[data-chart^="stress-"] path').length===4);
assert.ok((await page.locator('#stress-tiles').innerText()).includes('Agentes simulados online'));
const chart=page.locator('[data-chart="stress-requests"] svg');await chart.scrollIntoViewIfNeeded();const box=await chart.boundingBox();
await page.mouse.move(box.x+box.width-12,box.y+box.height*0.5);
await page.waitForFunction(()=>!document.querySelector('[data-chart="stress-requests"] .sophus-chart-tip').classList.contains('hidden'));
assert.ok((await page.locator('[data-chart="stress-requests"] .sophus-chart-tip').innerText()).includes('req/s'));
await page.locator('#stress-agent').selectOption('9');await page.locator('#stress-count').fill('20000');await page.locator('#stress-submit').click();
await page.waitForFunction(()=>document.getElementById('stress-job-status').textContent==='Concluído',null,{timeout:15000});
await page.locator('#stress-sim-agents').fill('20');await page.locator('#stress-sim-instances').fill('5');await page.locator('#stress-sim-rate').fill('30');
assert.ok((await page.locator('#stress-sim-estimate').innerText()).includes('150 mensagens por minuto'));
await page.locator('#stress-sim-submit').click();
await page.waitForFunction(()=>document.getElementById('stress-sim-status').textContent==='Em execução');
assert.equal(await page.locator('#stress-sim-stop').isVisible(),true);assert.ok((await page.locator('#stress-sim-details').innerText()).includes('20 de 20'));
await page.locator('#stress-sim-stop').click();await page.waitForFunction(()=>document.getElementById('stress-sim-status').textContent==='Encerrada');
await page.locator('#stress-contacts button',{hasText:'Remover'}).click();await page.waitForFunction(()=>document.getElementById('stress-job-title').textContent.includes('Remoção'));
await page.locator('#stress-revert-all').check();await page.locator('#stress-revert').click();
await page.waitForFunction(()=>document.getElementById('stress-job-title').textContent==='Reversão do teste');
await page.waitForFunction(()=>document.getElementById('stress-job-status').textContent==='Concluído',null,{timeout:15000});
const paths=posts.map(p=>p[0].replace('/management/api/stress-test',''));
assert.deepEqual(paths,['/messages','/simulation','/simulation/stop','/revert']);
assert.deepEqual(posts[0][1],{companyId:2,agentId:9,count:20000});
assert.deepEqual(posts[1][1],{companyId:2,agents:20,instances:5,messagesPerMinute:30,durationMinutes:10,agentIntervalSeconds:15});
assert.deepEqual(posts[3][1],{companyId:0},'global revert did not use the all-companies scope');
assert.deepEqual(deleted,['/management/api/stress-test/companies/2/contacts/44']);
assert.deepEqual(errors,[]);console.log('PASS: stress panel filter, generation, simulation, metrics charts, cleanup and revert');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(html.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
