package web

import (
	"context"
	"strings"
	"testing"
)

func TestSupportUIIncludesEntryAndReturnActions(t *testing.T) {
	var dashboard strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &dashboard); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Acessar como suporte", "/support'", "startCompanySupport"} {
		if !strings.Contains(dashboard.String(), value) {
			t.Fatalf("missing support action: %s", value)
		}
	}
	var banner strings.Builder
	if err := SupportSessionBanner().Render(context.Background(), &banner); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Modo de suporte", "Voltar ao super admin", `method="post"`, `action="/management/api/support/stop"`} {
		if !strings.Contains(banner.String(), value) {
			t.Fatalf("missing support return: %s", value)
		}
	}
}

func TestSupportBannerShowsScopedCompany(t *testing.T) {
	runControlDOMTest(t, SupportSessionBanner(), `
const assert=require('node:assert/strict'),{JSDOM}=require(process.env.ZUBLY_TEST_JSDOM);
const dom=new JSDOM(require('node:fs').readFileSync(0,'utf8'),{runScripts:'outside-only',url:'https://zubly.test/messages'}),w=dom.window,d=w.document;
w.fetch=async()=>({ok:true,status:200,json:async()=>({data:{id:'support-session',companyId:3,companyName:'Empresa selecionada',expiresAt:new Date(Date.now()+3600000).toISOString()}})});
for(const script of d.querySelectorAll('script'))w.eval(script.textContent);
(async()=>{await new Promise(resolve=>setTimeout(resolve,20));assert.equal(d.getElementById('support-session-banner').hidden,false);assert.ok(d.getElementById('support-company-name').textContent.includes('Empresa selecionada'));assert.equal(d.querySelector('form').method,'post');assert.ok(d.querySelector('form').action.endsWith('/management/api/support/stop'));dom.window.close();})().catch(error=>{console.error(error);process.exitCode=1;dom.window.close();});
`)
}
