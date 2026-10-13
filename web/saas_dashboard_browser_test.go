package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSaaSDashboardLoadsCompaniesPlansAndAddonsBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for super-admin browser regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var html strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=require('node:fs').readFileSync(0,'utf8');
(async()=>{const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage();const errors=[];let overviewCalls=0;
page.on('pageerror',error=>errors.push(error.stack||error.message));
await page.route('**/*',async route=>{const url=new URL(route.request().url());
 if(url.hostname!=='ui.test')return route.fulfill({status:204});
 if(url.pathname==='/admin')return route.fulfill({contentType:'text/html',body:html.replace('</head>','<style>.hidden{display:none!important}.flex{display:flex}@media(min-width:1024px){.saas-shell{display:grid}#saas-sidebar-toggle{display:grid!important}}</style></head>')});
 if(url.pathname==='/management/api/overview'){overviewCalls++;return route.fulfill({json:{data:{companies:[{id:1,name:'Empresa teste',email:'empresa@example.test',isActive:true,timezone:'UTC',currentPlanId:1,currentPlanName:'Plano teste',subscriptionStatus:'active',billingCycle:'monthly',priceCents:9900,currentPeriodEnd:'2026-11-01T00:00:00Z'}],plans:[{id:1,name:'Plano teste',description:'Plano com adicionais',monthlyPriceCents:9900,annualPriceCents:99000,agentLimit:3,connectionLimit:2,storageLimitGB:5,trialDays:7,isActive:true,allowAddons:true}],settings:{},addonPrices:{extraConnectionPriceCents:2000,extraAgentPriceCents:1000,extraStorageGBPriceCents:500,audioTranscriptionPriceCents:1490}}}});}
  if(url.pathname==='/management/api/addon-prices')return route.fulfill({json:{data:route.request().postDataJSON()}});
  if(url.pathname==='/management/api/payments')return route.fulfill({json:{data:[{companyName:'Empresa teste',payment:{id:10,companyId:1,status:'pending',provider:'mercado_pago',amountCents:9900,dueDate:'2026-11-01T00:00:00Z'}},{companyName:'Empresa teste',payment:{id:11,companyId:1,status:'pending',provider:'abacate_pay',amountCents:9900,dueDate:'2026-11-01T00:00:00Z',checkoutURL:'https://checkout.example.test/pix'}}]}});
 return route.fulfill({json:{data:[]}});
});
await page.goto('http://ui.test/admin');await page.waitForTimeout(150);
assert.deepEqual(errors,[],'dashboard initialization failed before loading companies and plans');assert.ok(overviewCalls>0,'overview request never started');
await page.waitForFunction(()=>document.getElementById('metric-companies').textContent==='1');
assert.equal(await page.locator('#companies-table-body tr').count(),1);assert.equal(await page.locator('#plans-grid article').count(),1);
assert.equal(await page.locator('#saas-sidebar-toggle').getAttribute('aria-expanded'),'true');await page.locator('#saas-sidebar-toggle').click();
assert.equal(await page.locator('#saas-sidebar-toggle').getAttribute('aria-expanded'),'false');assert.equal(await page.evaluate(()=>localStorage.getItem('sophus-admin-sidebar-expanded')),'false');
assert.ok(await page.locator('.saas-shell > aside').evaluate(el=>el.getBoundingClientRect().width)<=81);assert.equal(await page.locator('[data-saas-tab="plans"] > span').first().isVisible(),false);
await page.reload();await page.waitForFunction(()=>document.getElementById('metric-companies').textContent==='1');assert.equal(await page.locator('#saas-sidebar-toggle').getAttribute('aria-expanded'),'false');
await page.locator('#saas-sidebar-toggle').click();assert.equal(await page.locator('[data-saas-tab="plans"] > span').first().isVisible(),true);
await page.locator('[data-saas-tab="plans"]').click();assert.ok((await page.locator('#plans-grid').innerText()).includes('Adicionais permitidos'));
assert.equal(await page.locator('#addon-transcription-price').inputValue(),'R$ 14,90');assert.equal(await page.locator('#addon-prices-fields').isDisabled(),false);
 await page.locator('#addon-prices-submit').click();await page.waitForFunction(()=>document.getElementById('saas-toast-text').textContent.includes('adicionais salvos'));
 await page.locator('[data-saas-tab="invoices"]').click();await page.waitForFunction(()=>document.querySelectorAll('#invoices-list article').length===2);
 await page.locator('#invoices-list article').nth(0).getByRole('button',{name:'Editar',exact:true}).click();
 assert.equal(await page.locator('#invoice-edit-modal').isVisible(),true);assert.equal(await page.locator('#invoice-edit-submit').isVisible(),true);assert.equal(await page.locator('#invoice-cancel-button').textContent(),'Cancelar fatura');
 assert.ok((await page.locator('#invoice-edit-checkout-help').textContent()).includes('Mercado Pago'));assert.equal(await page.locator('#invoice-edit-form').getAttribute('data-payment-id'),'10');
 await page.keyboard.press('Escape');
 await page.locator('#invoices-list article').nth(1).getByRole('button',{name:'Editar',exact:true}).click();
 assert.equal(await page.locator('#invoice-edit-modal').isVisible(),true);assert.equal(await page.locator('#invoice-edit-submit').isVisible(),false);assert.equal(await page.locator('#invoice-cancel-button').textContent(),'Descartar cobrança');
 assert.ok((await page.locator('#invoice-edit-checkout-help').textContent()).includes('checkout PIX antigo'));assert.equal(await page.locator('#invoice-edit-form').getAttribute('data-payment-id'),'11');
 assert.deepEqual(errors,[]);console.log('PASS: super-admin initialization, company/plan lists, add-on prices and invoice editor for Mercado Pago and PIX');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("super-admin regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
