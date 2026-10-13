package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAddonManagerPurchaseAndPlanRestrictionBrowser(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT for add-on purchase regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var html strings.Builder
	if err := SubscriptionAddonManager().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT),html=require('node:fs').readFileSync(0,'utf8');
(async()=>{const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
try{const page=await browser.newPage();const errors=[];let purchases=0,lastBody;
const state={companyId:1,subscriptionId:1,planName:'Plano com adicionais',allowed:true,billingCycle:'monthly',currentPeriodEnd:new Date(Date.now()+15*86400000).toISOString(),quantities:{extraConnections:0,extraAgents:0,extraStorageGB:0,audioTranscription:false},prices:{extraConnectionPriceCents:2000,extraAgentPriceCents:1000,extraStorageGBPriceCents:500,audioTranscriptionPriceCents:1490},contractPrices:{},basePriceCents:10000,totalPriceCents:10000,limits:{agentLimit:2,connectionLimit:1,storageLimitGB:1},pendingPaymentId:null};
page.on('pageerror',error=>errors.push(error.message));
await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:'<!doctype html><html><head><meta charset="UTF-8"><style>.hidden{display:none!important}dialog{width:700px;max-height:90vh}fieldset{padding:12px}input{margin:8px}</style></head><body>'+html+'</body></html>'});
if(url.pathname.includes('/addons')){if(route.request().method()==='PUT'){purchases++;lastBody=route.request().postDataJSON();return route.fulfill({json:{data:{addons:{...state,quantities:lastBody,totalPriceCents:state.totalPriceCents+2000},payment:{status:'paid',amountCents:0}}}});}return route.fulfill({json:{data:{...state,allowed:!url.pathname.includes('/blocked/')}}});}
return route.fulfill({status:204});});
await page.goto('http://ui.test/');await page.evaluate(()=>window.openSubscriptionAddons('/management/api/billing/addons','Adicionais do meu plano'));
await page.waitForFunction(()=>!document.getElementById('subscription-addons-fields').disabled);assert.equal(await page.locator('#subscription-addons-submit').isEnabled(),false);
await page.locator('#subscription-addon-storage').fill('4');assert.ok((await page.locator('#subscription-addons-summary').innerText()).replace(/\u00a0/g,' ').includes('R$ 20,00'));assert.equal(await page.locator('#subscription-addons-submit').isEnabled(),true);
await page.locator('#subscription-addons-submit').click();await page.waitForFunction(()=>!document.getElementById('subscription-addons-dialog').open);assert.equal(purchases,1);assert.deepEqual(lastBody,{extraConnections:0,extraAgents:0,extraStorageGB:4,audioTranscription:false});
await page.evaluate(()=>window.openSubscriptionAddons('/management/api/companies/1/addons','Adicionais do cliente'));await page.waitForFunction(()=>!document.getElementById('subscription-addons-fields').disabled);await page.locator('#subscription-addon-connections').fill('1');await page.locator('#subscription-addons-submit').click();await page.waitForFunction(()=>!document.getElementById('subscription-addons-dialog').open);assert.equal(purchases,2);
await page.evaluate(()=>window.openSubscriptionAddons('/management/api/blocked/addons','Plano sem adicionais'));const blockedText=await page.locator('#subscription-addons-error').textContent();assert.ok(blockedText.includes('não permite'),'blocked plan feedback: '+blockedText+' errors='+JSON.stringify(errors));assert.equal(await page.locator('#subscription-addon-storage').isDisabled(),true);assert.equal(await page.locator('#subscription-addons-submit').isEnabled(),false);
assert.deepEqual(errors,[]);console.log('PASS: customer and super-admin quantities, estimates, submit payload and plan restriction');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(html.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("addon UI regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
