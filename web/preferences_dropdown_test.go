package web

import "testing"

func TestScrollPreferenceDropdownKeepsLoadedAndSavedValues(t *testing.T) {
	runControlDOMTest(t, AgentPreferencesPage(), `
const assert=require('node:assert/strict'); const {JSDOM}=require(process.env.ZUBLY_TEST_JSDOM);
const dom=new JSDOM(require('node:fs').readFileSync(0,'utf8'),{runScripts:'outside-only',url:'https://zubly.test/preferences'});
const w=dom.window,d=w.document; w.CSS={escape:value=>value};
const preferences={scrollAfterSend:true,scrollAfterSendMode:'except_quotes',messageSignatureEnabled:true,messageSignatureTemplate:'{agentName}',notifications:{toast:true,sound:true,browser:true,preview:true,groups:true}};
w.agentPreferencesReady=Promise.resolve(preferences); let saved;
w.applyAgentPreferences=value=>{saved=value;};
w.fetch=async(url,options)=>({ok:true,json:async()=>({data:JSON.parse(options.body)})});
for(const script of d.querySelectorAll('script')) if(script.textContent.includes('window.showAppToast =')||script.textContent.includes('function enhanceAdminSelect(')||script.textContent.includes("const form = document.getElementById('agent-preferences-form')")) w.eval(script.textContent);
(async()=>{
 await new Promise(resolve=>setTimeout(resolve,20));
 const mode=d.getElementById('message-scroll-after-send-mode'),toggle=d.getElementById('message-scroll-after-send');
 assert.equal(mode.dataset.adminEnhanced,'true'); assert.equal(mode._adminTrigger.textContent,'Exceto em citações');
 mode._adminTrigger.click(); const menu=d.getElementById(mode._adminTrigger.getAttribute('aria-controls'));
 assert.equal(menu.parentElement,d.body,'dropdown must not be clipped by its preference card');
 Array.from(menu.querySelectorAll('button')).find(button=>button.textContent==='Sempre rolar para baixo').click();
 assert.equal(mode.value,'always'); assert.equal(mode._adminTrigger.textContent,'Sempre rolar para baixo');
 const form=d.getElementById('agent-preferences-form'); form.dispatchEvent(new w.Event('submit',{bubbles:true,cancelable:true}));
 await new Promise(resolve=>setTimeout(resolve,20)); assert.equal(saved.scrollAfterSendMode,'always');
 assert.equal(d.getElementById('app-toast-text').textContent,'Preferências salvas.');assert.equal(d.getElementById('app-toast').classList.contains('hidden'),false);assert.equal(d.getElementById('agent-preferences-status').textContent,'');
 toggle.checked=false; toggle.dispatchEvent(new w.Event('change',{bubbles:true}));
 assert.equal(mode.disabled,true); assert.equal(mode._adminTrigger.disabled,true);
 toggle.checked=true; toggle.dispatchEvent(new w.Event('change',{bubbles:true})); assert.equal(mode._adminTrigger.disabled,false);
 dom.window.close();
})().catch(error=>{console.error(error);process.exitCode=1;dom.window.close();});
`)
}

func TestInstanceOfficialAPIOptionCannotBeSelected(t *testing.T) {
	runControlDOMTest(t, InstanceCreatePopup(), `
const assert=require('node:assert/strict'); const {JSDOM}=require(process.env.ZUBLY_TEST_JSDOM);
const dom=new JSDOM(require('node:fs').readFileSync(0,'utf8'),{runScripts:'outside-only'});
const d=dom.window.document,official=d.getElementById('deptApi'),qr=d.getElementById('deptQr');
assert.equal(official.disabled,true); const label=d.querySelector('label[for="deptApi"]');
assert.equal(label.getAttribute('aria-disabled'),'true'); assert.ok(label.textContent.includes('Indisponível'));
label.click(); assert.equal(official.checked,false);
d.querySelector('label[for="deptQr"]').click(); assert.equal(qr.checked,true);
label.click(); assert.equal(qr.checked,true); assert.equal(official.checked,false);
assert.equal(new dom.window.FormData(qr.form).get('connection_type'),'whatsapp_qrcode');
dom.window.close();
`)
}
