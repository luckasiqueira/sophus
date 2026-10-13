package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLoginDoesNotShowFailureDuringSuccessfulRedirect(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the login event regression check")
	}
	var html strings.Builder
	if err := Login().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	content := html.String()
	start := strings.LastIndex(content, "<script>") + len("<script>")
	end := strings.Index(content[start:], "</script>")
	if end < 0 {
		t.Fatal("login script missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert = require('node:assert/strict');
const vm = require('node:vm');
const callbacks = {};
const classes = new Set(['hidden']);
const error = {textContent:'', classList:{add:value=>classes.add(value),remove:value=>classes.delete(value)}};
const form = {addEventListener:(name,callback)=>callbacks[name]=callback};
vm.runInNewContext(require('node:fs').readFileSync(0,'utf8'), {document:{getElementById:id=>id==='login-form'?form:error}});
function respond(status,redirect,body) {
 callbacks['htmx:afterRequest']({detail:{xhr:{status,responseText:body||'',getResponseHeader:name=>name==='HX-Redirect'?redirect:null}}});
}
respond(204,'/'); assert.equal(classes.has('hidden'),true,'redirect without HTMX successful flag showed error');
respond(200,null); assert.equal(classes.has('hidden'),true,'HTTP success showed error');
respond(401,null,'{"error":"Email ou senha incorretos."}'); assert.equal(classes.has('hidden'),false); assert.equal(error.textContent,'Email ou senha incorretos.');
respond(204,'/'); assert.equal(classes.has('hidden'),true,'successful retry did not clear failure');
respond(0,null); assert.equal(classes.has('hidden'),false,'network failure must remain visible');
`)
	command.Stdin = strings.NewReader(content[start : start+end])
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("login event regression: %v\n%s", err, output)
	}
}
