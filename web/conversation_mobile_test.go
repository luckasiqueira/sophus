package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestConversationMobileHeaderLayoutAndActions(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PLAYWRIGHT") == "" || os.Getenv("ZUBLY_TEST_TAILWIND_CSS") == "" {
		t.Skip("set ZUBLY_TEST_PLAYWRIGHT and ZUBLY_TEST_TAILWIND_CSS for responsive browser checks")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	owner := 7
	conversation := repo.Conversation{Id: 10, Protocol: 1002, URL: uuid.New(), Status: repo.ConversationStatusOpen, ConnectionID: 3, AgentID: &owner, AgentName: "Marina Souza", DepartmentName: "Suporte comercial", Contact: repo.Contact{Id: 1, Name: "João Carvalho da Silva — nome de contato longo", Number: "5511999999999"}}
	var html strings.Builder
	messages := []repo.MessageData{{MessageId: "mobile-quote", ConversationId: conversation.Id, Text: "Posso enviar o comprovante?", MediaType: "text", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if err := MessageSingle(conversation, repo.Agent{Id: owner, CompanyId: 2, Role: repo.RoleAdmin}, nil, messages, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),fs=require('node:fs'),{chromium}=require(process.env.ZUBLY_TEST_PLAYWRIGHT);
const html=fs.readFileSync(0,'utf8'),css=fs.readFileSync(process.env.ZUBLY_TEST_TAILWIND_CSS,'utf8');
const scripts=Array.from(html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)).map(match=>match[1]).filter(source=>source.includes('window.conversationAbort = controller')||source.includes("const toggle = document.getElementById('conversation-actions-toggle')")||source.includes('window.openCloseConversationModal =')||source.includes("const exportMenu = document.getElementById('conversation-export-menu')")||source.includes('window.syncMessageComposerLayout =')||source.includes('const quotedInput = composer.querySelector')||source.includes('function initializeConversationNotes()')||source.includes("const button = document.getElementById('message-emoji-button')")||source.includes("const attachmentButton = document.getElementById('message-attachment-button')"));
const markup=html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi,'');
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.ZUBLY_TEST_CHROME||'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',headless:true});
 try {
  const page=await browser.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/*',route=>route.fulfill({status:200,body:''}));
  for(const width of [320,390,768,1280]) {
   await page.setViewportSize({width,height:844});await page.setContent(markup);await page.addStyleTag({content:css});
   for(const source of scripts)await page.evaluate(source=>window.eval(source),source);
   const toggle=page.locator('#conversation-actions-toggle'),actions=page.locator('#conversation-header-actions');
   assert.equal(await toggle.isVisible(),width<1024);
   assert.equal(await actions.isVisible(),width>=1024,'actions must be collapsed only on mobile/tablet');
   assert.equal(await page.getByLabel('Protocolo 1002',{exact:true}).isVisible(),true);
   const area=page.locator('#message-composer-area');
   assert.equal(await area.getAttribute('data-composer-layout'),width<1024?'actions':'desktop');
   const dimensions=await page.evaluate(()=>({width:document.documentElement.scrollWidth,header:document.getElementById('conversation-chat-header').getBoundingClientRect().height}));
   assert.ok(dimensions.width<=width,'page has horizontal overflow at '+width);
   if(width<1024){
    assert.ok(dimensions.header<=110,'mobile header is too tall');
    await toggle.click();assert.equal(await actions.isVisible(),true);assert.equal(await toggle.getAttribute('aria-expanded'),'true');
    const rect=await actions.boundingBox();assert.ok(rect.x>=0&&rect.x+rect.width<=width,'actions overflow the viewport');
    await actions.locator('#conversation-export-menu summary').click();assert.equal(await actions.isVisible(),true,'export submenu must keep parent open');
    await page.keyboard.press('Escape');assert.equal(await actions.isVisible(),false);
    await toggle.click();await actions.getByTitle('Fechar conversa',{exact:true}).click();
    assert.equal(await actions.isVisible(),false);assert.equal(await page.locator('#close-conversation-modal').isVisible(),true,'close action must still open its confirmation');
    await page.locator('#cancel-close-conversation').click();
    await toggle.click();const chatRect=await page.locator('#chatMessages').boundingBox();await page.locator('#chatMessages').click({position:{x:20,y:chatRect.height-20}});assert.equal(await actions.isVisible(),false,'outside click must close actions');
    const attachment=page.locator('#message-attachment-button'),input=page.locator('#message-composer [name=message]');
    assert.equal(await page.locator('#message-record-button').isVisible(),false,'default layout must hide secondary actions');
    const inputRect=await input.boundingBox();assert.ok(inputRect.width>=150,'default message field is too narrow');
    await attachment.click();assert.equal(await page.locator('#message-gallery-option').isVisible(),true);assert.equal(await page.locator('#message-record-button').isVisible(),true);
    await page.locator('#message-emoji-button').click();assert.equal(await page.locator('#message-emoji-tray').isVisible(),true);
    await page.getByRole('button',{name:'Inserir 😀',exact:true}).click();assert.equal(await input.inputValue(),'😀');
    await attachment.click();await page.locator('#conversation-note-button').click();assert.equal(await page.locator('#conversation-note-form').isVisible(),true);assert.equal(await area.isVisible(),false);
    await page.locator('#conversation-note-cancel').click();assert.equal(await area.isVisible(),true);assert.equal(await page.evaluate(()=>document.activeElement.id),'message-attachment-button');
    await page.locator('[data-reply-message-id="mobile-quote"]').click();
    assert.equal(await area.getAttribute('data-composer-layout'),'quote');assert.equal(await page.locator('#message-reply-preview').isVisible(),true);
    for(const id of ['conversation-note-button','message-emoji-button','message-record-button'])assert.equal(await page.locator('#'+id).isVisible(),true,'quote shortcut hidden: '+id);
    const quoteRect=await input.boundingBox(),formRect=await page.locator('#message-composer').boundingBox(),sendRect=await page.locator('#message-send-button').boundingBox();
    assert.ok(quoteRect.width>=formRect.width-1,'quoted message field must occupy the full width');assert.ok(sendRect.y>=quoteRect.y+quoteRect.height,'send button must be on the second row for quotes');
    await page.locator('#message-reply-cancel').click();assert.equal(await area.getAttribute('data-composer-layout'),'actions');assert.equal(await input.inputValue(),'😀','canceling quote must preserve draft');
    await page.locator('[data-reply-message-id="mobile-quote"]').click();
    await page.locator('#message-composer').evaluate(form=>{new Function(form.getAttribute('hx-on::before-request')).call(form);});
    assert.equal(await area.getAttribute('data-composer-layout'),'actions');assert.equal(await page.locator('#message-composer [name=quotedId]').inputValue(),'');assert.equal(await page.locator('#message-composer').getAttribute('data-sent-with-quote'),'true');
   }
  }
  assert.deepEqual(errors,[]);console.log('PASS: 320px, 390px, tablet and desktop; actions drawer, emoji, notes, quote layout, cancel/send reset and header actions');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
`)
	command.Stdin = strings.NewReader(html.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("mobile chat browser check: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
