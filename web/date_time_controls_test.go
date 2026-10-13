package web

import "testing"

func TestSharedMediaCalendarInteractions(t *testing.T) {
	runControlDOMTest(t, MediaLibrary(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0, 'utf8'), {runScripts:'outside-only', url:'https://zubly.test/media'});
const w = dom.window, d = w.document;
w.CSS = {escape: value => value};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('function enhanceAdminSelect(') || script.textContent.includes("const calendar = document.getElementById('report-calendar')")) w.eval(script.textContent);
const from = d.getElementById('media-date-from'), to = d.getElementById('media-date-to');
const calendar = d.getElementById('report-calendar');
const trigger = input => input.parentElement.querySelector('[data-calendar-trigger]');
from.value='2024-02-27'; to.value='2024-03-02';
let changes = 0; from.addEventListener('change', () => changes++);
trigger(from).click();
assert.equal(calendar.hidden, false);
assert.equal(calendar.querySelector('[data-calendar-month]').dataset.adminEnhanced, 'true');
const month = calendar.querySelector('[data-calendar-month]');
month._adminTrigger.click();
const menu = d.getElementById(month._adminTrigger.getAttribute('aria-controls'));
assert.equal(menu.parentElement, d.body, 'month menu must not be clipped by the calendar');
assert.equal(menu.classList.contains('hidden'), false);
Array.from(menu.querySelectorAll('button')).find(button => button.textContent === 'Março').click();
assert.equal(month.value, '2');
assert.equal(calendar.hidden, false, 'choosing a month must keep the calendar open');
assert.equal(month._adminTrigger.textContent, 'Março');
assert.equal(menu.classList.contains('hidden'), true, 'only the month dropdown should close');
const year = calendar.querySelector('[data-calendar-year]');
year._adminTrigger.click();
const yearMenu = d.getElementById(year._adminTrigger.getAttribute('aria-controls'));
Array.from(yearMenu.querySelectorAll('button')).find(button => button.textContent === '2025').click();
assert.equal(calendar.hidden, false, 'choosing a year must keep the calendar open');
assert.equal(year.value, '2025'); assert.equal(month.value, '2');
assert.ok(calendar.querySelector('[data-date="2025-03-15"]'));
year._adminTrigger.click();
Array.from(yearMenu.querySelectorAll('button')).find(button => button.textContent === '2024').click();
calendar.querySelector('[data-date="2024-03-15"]').click();
assert.equal(from.value, '2024-03-15'); assert.equal(to.value, '2024-03-15'); assert.equal(changes, 1);
trigger(to).click(); calendar.querySelector('[data-calendar-prev]').click();
assert.equal(calendar.querySelector('[data-date="2024-02-28"]').disabled, true);
calendar.dispatchEvent(new w.KeyboardEvent('keydown', {key:'Escape', bubbles:true}));
assert.equal(calendar.hidden, true); assert.equal(d.activeElement, trigger(to));
const cleanup = d.getElementById('media-cleanup-date');
cleanup.value = '2024-02-29'; trigger(cleanup).click();
assert.equal(calendar.querySelector('[data-date="2024-02-29"]').getAttribute('aria-selected'), 'true');
calendar.querySelector('[data-calendar-clear]').click(); assert.equal(cleanup.value, '');
dom.window.close();
`)
}

func TestSharedTimeAndDynamicControls(t *testing.T) {
	runControlDOMTest(t, CompanySettings(), `
const assert = require('node:assert/strict');
const { JSDOM } = require(process.env.ZUBLY_TEST_JSDOM);
const dom = new JSDOM(require('node:fs').readFileSync(0,'utf8'), {runScripts:'outside-only',url:'https://zubly.test/settings'});
const w=dom.window,d=w.document; w.CSS={escape:value=>value};
for (const script of d.querySelectorAll('script')) if (script.textContent.includes('function enhanceAdminSelect(') || script.textContent.includes("const calendar = document.getElementById('report-calendar')")) w.eval(script.textContent);
const input=d.getElementById('company-hours-start'); input.name='start'; input.value='08:30'; input.min='08:00'; input.max='18:00'; input.step='900';
const trigger=input.parentElement.querySelector('[data-calendar-trigger]'); const picker=d.getElementById('app-time-picker');
let changes=0; input.addEventListener('change',()=>changes++);
trigger.click(); assert.equal(picker.hidden,false);
const hour=picker.querySelector('[data-time-hour]'), minute=picker.querySelector('[data-time-minute]');
assert.equal(hour.value,'08'); assert.equal(minute.value,'30');
hour._adminTrigger.click();
const hourMenu=d.getElementById(hour._adminTrigger.getAttribute('aria-controls'));
Array.from(hourMenu.querySelectorAll('button')).find(button=>button.textContent==='09').click();
assert.equal(picker.hidden,false,'choosing an hour must keep the time picker open');
minute._adminTrigger.click();
const minuteMenu=d.getElementById(minute._adminTrigger.getAttribute('aria-controls'));
Array.from(minuteMenu.querySelectorAll('button')).find(button=>button.textContent==='45').click();
assert.equal(picker.hidden,false,'choosing a minute must keep the time picker open');
picker.querySelector('[data-time-apply]').click();
assert.equal(input.value,'09:45'); assert.equal(changes,1); assert.equal(picker.hidden,true);
assert.equal(new w.FormData(input.form).get('start'),'09:45');
trigger.click(); minute.value='47'; picker.querySelector('[data-time-apply]').click();
assert.equal(input.value,'09:45','invalid steps must not overwrite the value'); assert.equal(picker.querySelector('[data-time-error]').hidden,false);
hour.value='19'; minute.value='00'; picker.querySelector('[data-time-apply]').click(); assert.equal(input.value,'09:45','limits must be enforced');
picker.querySelector('[data-time-close]').click(); assert.equal(d.activeElement,trigger);
(async()=>{
 const dynamic=d.createElement('input'); dynamic.type='time'; dynamic.step='1'; dynamic.value='10:15:30'; d.body.append(dynamic);
 await new Promise(resolve=>setTimeout(resolve,20));
 assert.equal(dynamic.dataset.dateTimeEnhanced,'true','flow-builder and HTMX inputs must be enhanced');
 dynamic.parentElement.querySelector('[data-calendar-trigger]').click();
 assert.equal(picker.querySelector('[data-time-seconds]').hidden,false);
 picker.querySelector('[data-time-second]').value='45'; picker.querySelector('[data-time-apply]').click(); assert.equal(dynamic.value,'10:15:45');
 dynamic.disabled=true; await new Promise(resolve=>setTimeout(resolve,10)); assert.equal(dynamic.parentElement.querySelector('[data-calendar-trigger]').disabled,true);
 dom.window.close();
})().catch(error=>{console.error(error);process.exitCode=1;dom.window.close();});
`)
}

func TestTagEditorSaveAndErrorInteractions(t *testing.T) {
	runControlDOMTest(t, Contacts(), `
const assert=require('node:assert/strict'); const {JSDOM}=require(process.env.ZUBLY_TEST_JSDOM);
const dom=new JSDOM(require('node:fs').readFileSync(0,'utf8'),{runScripts:'outside-only',url:'https://zubly.test/contacts'});
const w=dom.window,d=w.document,requests=[]; w.CSS={escape:value=>value}; w.HTMLElement.prototype.scrollIntoView=function(){}; let reject=false;
w.fetch=async (url,options={})=>{requests.push({url:String(url),...options}); return {ok:!reject,status:reject?409:200,text:async()=>JSON.stringify(reject?{error:'Já existe uma tag com esse nome.'}:{data:String(url).endsWith('/contact-tags')?[{id:3,name:'Clientes',contactCount:2}]:[],total:0,page:1})};};
for(const script of d.querySelectorAll('script')) if(script.textContent.includes('function enhanceAdminSelect(')||script.textContent.includes('window.contactRequest =')||script.textContent.includes('const contactsState =')) w.eval(script.textContent);
(async()=>{
 await new Promise(resolve=>setTimeout(resolve,30));
 const edit=Array.from(d.querySelectorAll('#contact-tags-list button')).find(button=>button.textContent.includes('Editar')); edit.focus(); edit.click();
 const modal=d.getElementById('contact-tag-edit-modal'), input=d.getElementById('contact-tag-edit-name'), form=d.getElementById('contact-tag-edit-form');
 assert.equal(modal.classList.contains('hidden'),false); assert.equal(input.value,'Clientes'); assert.equal(d.activeElement,input);
 input.value=''; form.dispatchEvent(new w.Event('submit',{bubbles:true,cancelable:true})); assert.equal(d.getElementById('contact-tag-edit-error').classList.contains('hidden'),false);
 reject=true; input.value='Duplicada'; form.dispatchEvent(new w.Event('submit',{bubbles:true,cancelable:true})); await new Promise(resolve=>setTimeout(resolve,20));
 assert.equal(modal.classList.contains('hidden'),false); assert.ok(d.getElementById('contact-tag-edit-error').textContent.includes('Já existe')); assert.equal(d.getElementById('contact-tag-edit-save').disabled,false);
 reject=false; input.value='  VIP  '; form.dispatchEvent(new w.Event('submit',{bubbles:true,cancelable:true})); await new Promise(resolve=>setTimeout(resolve,30));
 assert.equal(modal.classList.contains('hidden'),true); assert.ok(requests.some(request=>request.method==='PUT'&&request.url.endsWith('/contact-tags/3')&&JSON.parse(request.body).name==='VIP'));
 const nextEdit=Array.from(d.querySelectorAll('#contact-tags-list button')).find(button=>button.textContent.includes('Editar')); nextEdit.focus(); nextEdit.click();
 modal.dispatchEvent(new w.KeyboardEvent('keydown',{key:'Escape',bubbles:true})); assert.equal(modal.classList.contains('hidden'),true); assert.equal(d.activeElement,nextEdit);
 assert.ok(!requests.some(request=>request.method==='DELETE'),'editing must not remove tags'); dom.window.close();
})().catch(error=>{console.error(error);process.exitCode=1;dom.window.close();});
`)
}
