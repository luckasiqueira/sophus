package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGuidedOnboardingFirstAccessResumeAndMobileBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for guided onboarding tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	var html strings.Builder
	if err := base().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),html=require('node:fs').readFileSync(0,'utf8');
const css='<style>.hidden{display:none!important}.flex{display:flex}.gap-2{gap:8px}.mt-5{margin-top:20px}.app-shell{display:flex;height:100vh;box-sizing:border-box;padding:12px;gap:12px}#app-navigation{display:flex;flex-direction:column;flex-shrink:0;overflow-y:auto;padding:12px 0;box-sizing:border-box}#app-sidebar-header{display:flex;align-items:center;height:88px;flex-shrink:0}#app-menu-links{position:static;display:flex!important;flex-direction:column;gap:2px;padding:0 8px;box-sizing:border-box}#app-menu-links a{display:flex;gap:12px;padding:10px 12px;min-height:38px;box-sizing:border-box;align-items:center}#app-sidebar-footer{display:flex;margin-top:auto;gap:12px;padding:8px;align-items:center}#app-user-menu summary{display:block}#app-user-menu>div{position:fixed;background:white;right:12px;bottom:12px;width:230px;padding:12px}#app-user-menu a,#app-user-menu button{display:block;padding:10px}#app-menu-button{display:none}#app-notification-center-button{width:42px;height:42px}#app-sidebar-header img{max-width:100%;height:28px}#app-sidebar-header svg{width:32px;height:32px}@media(max-width:1023px){.app-shell{flex-direction:column;padding:8px}#app-navigation{height:58px;width:100%;overflow:visible;flex-direction:row;padding:8px;position:relative}#app-sidebar-header{display:none}#app-menu-button{display:block;height:40px;width:40px}#app-menu-links{position:absolute;left:0;right:0;top:66px;overflow-y:auto;background:#6123cc;border-radius:16px;padding:8px}#app-menu-links.hidden{display:none!important}#app-sidebar-footer{flex-direction:row!important;margin:0 0 0 auto;padding:0}#app-user-menu>div{top:72px;bottom:auto}#app-sidebar-footer .app-sidebar-expanded-only{display:none}}</style>';
(async()=>{const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{
 const page=await browser.newPage({viewport:{width:1366,height:900}});const errors=[];let onboarding={completed:false,step:0},admin=true,fail=false,saves=0;
 page.on('pageerror',error=>errors.push(error.message));await page.addInitScript(()=>{window.EventSource=class extends EventTarget{close(){}};localStorage.setItem('sophus-sidebar-expanded','false');});
 await page.route('**/*',route=>{const request=route.request(),url=new URL(request.url());if(url.hostname!=='ui.test')return route.fulfill({status:204});if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:html.replace('</head>',css+'</head>')});if(url.pathname==='/management/api/preferences')return route.fulfill({json:{isAdmin:admin,data:{scrollAfterSend:true,scrollAfterSendMode:'always',messageSignatureEnabled:true,messageSignatureTemplate:'{agentName}',notifications:{toast:true,sound:false,browser:false,preview:true,groups:true},onboarding}}});if(url.pathname==='/management/api/onboarding'){saves++;if(fail)return route.fulfill({status:500,json:{error:'Falha simulada ao salvar'}});const state=request.postDataJSON();onboarding={completed:onboarding.completed||state.completed,step:Math.max(onboarding.step,state.step)};return route.fulfill({status:204});}return route.fulfill({status:204});});
 async function geometry(){await page.waitForTimeout(230);return page.evaluate(()=>{const box=document.getElementById('app-onboarding-card').getBoundingClientRect(),spot=document.getElementById('app-onboarding-spotlight').getBoundingClientRect();return{inside:box.left>=0&&box.top>=0&&box.right<=innerWidth+1&&box.bottom<=innerHeight+1,highlight:spot.width>20&&spot.height>20,overlap:Math.min(box.right,spot.right)>Math.max(box.left,spot.left)&&Math.min(box.bottom,spot.bottom)>Math.max(box.top,spot.top),title:document.getElementById('app-onboarding-title').textContent};});}
 await page.goto('http://ui.test/');await page.waitForFunction(()=>document.getElementById('app-onboarding').open);assert.equal(await page.locator('#app-onboarding-title').textContent(),'Conversas');assert.equal(await page.locator('#app-onboarding-back').isVisible(),false);assert.equal(await page.locator('#app-onboarding-counter').textContent(),'1 de 13');
 await page.locator('#app-onboarding-next').click();await page.waitForFunction(()=>document.getElementById('app-onboarding-title').textContent==='Contatos');await page.waitForTimeout(50);assert.equal(onboarding.step,1);
 await page.reload();await page.waitForFunction(()=>document.getElementById('app-onboarding').open);assert.equal(await page.locator('#app-onboarding-title').textContent(),'Contatos');await page.locator('#app-onboarding-back').click();assert.equal(await page.locator('#app-onboarding-title').textContent(),'Conversas');
 for(let i=0;i<13;i++){const state=await geometry();assert.equal(state.inside,true,'desktop card outside viewport: '+state.title);assert.equal(state.highlight,true);assert.equal(state.overlap,false,'card covers highlighted area: '+state.title);await page.locator('#app-onboarding-next').click();}
 await page.waitForFunction(()=>!document.getElementById('app-onboarding').open);assert.equal(onboarding.completed,true);assert.equal(await page.evaluate(()=>document.documentElement.dataset.sidebarExpanded),'false');assert.equal(await page.evaluate(()=>localStorage.getItem('sophus-sidebar-expanded')),'false');
 await page.reload();await page.waitForTimeout(150);assert.equal(await page.locator('#app-onboarding').evaluate(el=>el.open),false,'completed guide appeared again');await page.locator('#app-user-menu summary').click();await page.locator('#app-onboarding-replay').click();await page.waitForFunction(()=>document.getElementById('app-onboarding').open);assert.equal(await page.locator('#app-onboarding-title').textContent(),'Conversas');await page.locator('#app-onboarding-skip').click();await page.waitForFunction(()=>!document.getElementById('app-onboarding').open);
 onboarding={completed:false,step:0};admin=false;await page.setViewportSize({width:390,height:700});await page.reload();await page.waitForFunction(()=>document.getElementById('app-onboarding').open);assert.equal(await page.locator('#app-onboarding-counter').textContent(),'1 de 6');
 for(let i=0;i<6;i++){const state=await geometry();assert.equal(state.inside,true,'mobile card outside viewport: '+state.title);assert.equal(state.highlight,true);assert.equal(state.overlap,false,'mobile card covers highlighted area: '+state.title);await page.locator('#app-onboarding-next').click();}
 await page.waitForFunction(()=>!document.getElementById('app-onboarding').open);assert.equal(await page.locator('#app-menu-button').getAttribute('aria-expanded'),'false');assert.equal(onboarding.completed,true);
 onboarding={completed:false,step:0};fail=true;await page.reload();await page.waitForFunction(()=>document.getElementById('app-onboarding').open);await page.locator('#app-onboarding-skip').click();await page.locator('#app-onboarding-error').waitFor({state:'visible'});assert.equal(onboarding.completed,false);assert.equal(await page.locator('#app-onboarding-next').isEnabled(),true);fail=false;await page.locator('#app-onboarding-skip').click();await page.waitForFunction(()=>!document.getElementById('app-onboarding').open);
 assert.ok(saves>0);assert.deepEqual(errors,[]);console.log('PASS: first-access guide, resume/back, desktop/mobile highlights, viewport placement, completion, replay, menu restoration and save retry');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(html.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("onboarding browser regression: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
