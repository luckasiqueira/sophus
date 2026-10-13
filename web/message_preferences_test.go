package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestScrollPreferenceIsAvailableBeforeSettingsInitialize(t *testing.T) {
	var html strings.Builder
	if err := AgentPreferencesPage().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	content := html.String()
	preference := strings.Index(content, "window.shouldAutoScrollAfterSend =")
	checkbox := strings.Index(content, "const messageScrollAfterSend =")
	if preference < 0 || checkbox < 0 || preference > checkbox {
		t.Fatal("checkbox initializes before persistent preference is available")
	}
}

func TestDisabledScrollPreferenceSurvivesReload(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required for preference reload regression")
	}
	var html strings.Builder
	if err := MessagePreferences().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	script := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(html.String()), "<script>"), "</script>"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict');const vm=require('node:vm');const source=require('node:fs').readFileSync(0,'utf8');
const storage=new Map(); const localStorage={getItem:key=>storage.has(key)?storage.get(key):null,setItem:(key,value)=>storage.set(key,value)};
let window={};vm.runInNewContext(source,{window,localStorage});assert.equal(window.shouldAutoScrollAfterSend(),true);
window.setAutoScrollAfterSend(false);assert.equal(storage.get('sophus-scroll-after-send'),'false');assert.equal(window.shouldAutoScrollAfterSend(),false);
window={};vm.runInNewContext(source,{window,localStorage});assert.equal(window.shouldAutoScrollAfterSend(),false,'reload re-enabled scroll');
window.setAutoScrollAfterSend(true);assert.equal(window.shouldAutoScrollAfterSend(),true);
window.setAutoScrollAfterSendMode('except_quotes');assert.equal(window.shouldAutoScrollAfterSend(true),false);assert.equal(window.shouldAutoScrollAfterSend(false),true);
window={};vm.runInNewContext(source,{window,localStorage});assert.equal(window.getAutoScrollAfterSendMode(),'except_quotes');assert.equal(window.shouldAutoScrollAfterSend(true),false,'reload lost quote exception');
window.setAutoScrollAfterSendMode('always');assert.equal(window.shouldAutoScrollAfterSend(true),true);
`)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("preference reload regression: %v\n%s", err, output)
	}
}
