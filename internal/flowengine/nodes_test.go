package flowengine

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidateFlowDataAcceptsQuickWinNodes(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[
		{"id":"variable","type":"setVariable","data":{"variableName":"leadSource","value":"site"}},
		{"id":"tag","type":"tag","data":{"operation":"add","tag":"vip"}},
		{"id":"contact","type":"updateContact","data":{"field":"email","value":"{{email}}"}},
		{"id":"hours","type":"businessHours","data":{"timezone":"America/Sao_Paulo","days":[1,2,3,4,5],"startTime":"08:00","endTime":"18:00"}},
		{"id":"split","type":"randomSplit","data":{"percentage":50}}
	],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected quick-win nodes to be valid: %v", err)
	}
}

func TestValidateFlowDataAcceptsExtensionNodes(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[
		{"id":"field","type":"setContactField","data":{"fieldKey":"customerTier","fieldType":"text","value":"gold"}},
		{"id":"summary","type":"conversationSummary","data":{"credentialId":1,"maxMessages":30,"saveResponseTo":"conversationSummary"}},
		{"id":"nps","type":"npsEvaluation","data":{"message":"Dê uma nota de 0 a 10","saveScoreTo":"npsScore","timeout":3600}}
	],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected extension nodes to be valid: %v", err)
	}
}

func TestFlowExtensionHelpers(t *testing.T) {
	if got := httpResponseHandle(map[string]interface{}{"status": 204}); got != "success" {
		t.Fatalf("HTTP 204 handle = %q", got)
	}
	if got := httpResponseHandle(map[string]interface{}{"status": 500}); got != "failure" {
		t.Fatalf("HTTP 500 handle = %q", got)
	}
	if got := httpResponseHandle(map[string]interface{}{"error": true}); got != "failure" {
		t.Fatalf("HTTP error handle = %q", got)
	}
	for score, want := range map[int]string{0: "detractor", 6: "detractor", 7: "passive", 8: "passive", 9: "promoter", 10: "promoter"} {
		if got := npsCategory(score); got != want {
			t.Fatalf("npsCategory(%d) = %q, want %q", score, got, want)
		}
	}
	number, err := flowCustomFieldValue(FlowNode{Data: map[string]interface{}{"fieldType": "number", "value": "{{score}}"}}, ExecutionContext{"score": 9})
	if err != nil || number != float64(9) {
		t.Fatalf("numeric custom field = %#v, %v", number, err)
	}
}

func TestHTTPRequestRoutingKeepsLegacyContinuation(t *testing.T) {
	engine := &Engine{}
	edges := []FlowEdge{{Source: "http", Target: "next", SourceHandle: "success"}}
	node := FlowNode{ID: "http", Type: NodeHTTPRequest, Data: map[string]interface{}{}}

	handle := httpRequestOutputHandle(node, edges, "failure")
	if handle != "" {
		t.Fatalf("legacy HTTP failure handle = %q, want regular continuation", handle)
	}
	next, err := engine.getNextNode(node.ID, string(node.Type), edges, nil, nil, handle, "")
	if err != nil || next != "next" {
		t.Fatalf("legacy HTTP failure route = %q, %v, want next", next, err)
	}
}

func TestHTTPRequestRoutingUsesExplicitStatusBranches(t *testing.T) {
	engine := &Engine{}
	edges := []FlowEdge{
		{Source: "http", Target: "success-node", SourceHandle: "success"},
		{Source: "http", Target: "failure-node", SourceHandle: "failure"},
	}
	node := FlowNode{ID: "http", Type: NodeHTTPRequest, Data: map[string]interface{}{"routeByStatus": true}}

	handle := httpRequestOutputHandle(node, edges, "failure")
	next, err := engine.getNextNode(node.ID, string(node.Type), edges, nil, nil, handle, "")
	if err != nil || next != "failure-node" {
		t.Fatalf("branched HTTP failure route = %q, %v, want failure-node", next, err)
	}
}

func TestHTTPRequestRoutingInfersBranchesFromFailureEdge(t *testing.T) {
	edges := []FlowEdge{{Source: "http", Target: "failure-node", SourceHandle: "failure"}}
	node := FlowNode{ID: "http", Type: NodeHTTPRequest, Data: map[string]interface{}{}}

	if handle := httpRequestOutputHandle(node, edges, "failure"); handle != "failure" {
		t.Fatalf("inferred HTTP failure handle = %q, want failure", handle)
	}
}

func TestValidateFlowDataRejectsInvalidQuickWinNodes(t *testing.T) {
	tests := []string{
		`{"variableName":"_private","value":"x"}`,
		`{"operation":"replace","tag":"vip"}`,
		`{"field":"number","value":"123"}`,
		`{"percentage":100}`,
	}
	types := []string{"setVariable", "tag", "updateContact", "randomSplit"}
	for index, data := range tests {
		raw := json.RawMessage(`{"nodes":[{"id":"node","type":"` + types[index] + `","data":` + data + `}],"edges":[]}`)
		if err := ValidateFlowData(raw); err == nil {
			t.Fatalf("expected invalid %s node to be rejected", types[index])
		}
	}
}

func TestBusinessHoursOpenAt(t *testing.T) {
	hours := BusinessHours{
		Timezone: "America/Sao_Paulo", Days: []int{1}, StartTime: "22:00", EndTime: "06:00",
		LunchEnabled: true, LunchStart: "23:00", LunchEnd: "23:30",
	}
	location, err := time.LoadLocation(hours.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		now  time.Time
		open bool
	}{
		{name: "monday evening", now: time.Date(2026, 8, 24, 22, 30, 0, 0, location), open: true},
		{name: "lunch interval", now: time.Date(2026, 8, 24, 23, 15, 0, 0, location), open: false},
		{name: "overnight belongs to monday", now: time.Date(2026, 8, 25, 2, 0, 0, 0, location), open: true},
		{name: "after overnight range", now: time.Date(2026, 8, 25, 7, 0, 0, 0, location), open: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			open, err := businessHoursOpenAt(hours, test.now)
			if err != nil {
				t.Fatal(err)
			}
			if open != test.open {
				t.Fatalf("got open=%v, want %v", open, test.open)
			}
		})
	}
}

func TestRandomSplitHandleBoundaries(t *testing.T) {
	if handle := randomSplitHandle(30, 29); handle != "a" {
		t.Fatalf("got %q, want a", handle)
	}
	if handle := randomSplitHandle(30, 30); handle != "b" {
		t.Fatalf("got %q, want b", handle)
	}
}

func TestValidInputResponse(t *testing.T) {
	tests := []struct {
		response, messageType, validationType, pattern string
		received                                       bool
		valid                                          bool
	}{
		{response: "cliente@example.com", validationType: "email", valid: true},
		{response: "cliente", validationType: "email", valid: false},
		{response: "10,5", validationType: "number", valid: true},
		{response: "ABC-123", validationType: "regex", pattern: `^[A-Z]{3}-\d{3}$`, valid: true},
		{response: "", messageType: "image", validationType: "any", received: true, valid: true},
		{response: "", messageType: "image", validationType: "text", valid: false},
		{response: "", validationType: "any", received: true, valid: true},
		{response: "", validationType: "any", valid: false},
	}
	for _, test := range tests {
		if got := validInputResponse(test.response, test.messageType, test.received, test.validationType, test.pattern); got != test.valid {
			t.Fatalf("validInputResponse(%q, %q, %q) = %v, want %v", test.response, test.messageType, test.validationType, got, test.valid)
		}
	}
}

func TestMessageTypeHandle(t *testing.T) {
	if got := messageTypeHandle(" IMAGE "); got != "image" {
		t.Fatalf("got %q, want image", got)
	}
	if got := messageTypeHandle("interactive"); got != "other" {
		t.Fatalf("got %q, want other", got)
	}
}

func TestAIResponseParsing(t *testing.T) {
	response := map[string]interface{}{
		"status": 200,
		"data": map[string]interface{}{"choices": []interface{}{
			map[string]interface{}{"message": map[string]interface{}{"content": " resposta "}},
		}},
	}
	text, ok := aiResponseText(response)
	if !ok || text != "resposta" {
		t.Fatalf("aiResponseText() = %q, %v", text, ok)
	}
	errorResponse := map[string]interface{}{
		"status": 429,
		"data":   map[string]interface{}{"error": map[string]interface{}{"message": "limite excedido"}},
	}
	if got := aiResponseError(errorResponse); got != "limite excedido" {
		t.Fatalf("aiResponseError() = %q", got)
	}
}

func TestPublicFlowContextHidesInternalKeys(t *testing.T) {
	public := publicFlowContext(ExecutionContext{"name": "Ana", "_flowId": 1, "_secret": "x"})
	if public["name"] != "Ana" || public["_flowId"] != nil || public["_secret"] != nil {
		t.Fatalf("unexpected public context: %#v", public)
	}
}

func TestNamespaceFlowDataUsesBoundedStableIDs(t *testing.T) {
	longID := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJK"
	flowData := FlowData{
		Nodes: []FlowNode{{ID: longID, Type: NodeStart, Data: map[string]interface{}{}}},
		Edges: []FlowEdge{},
	}
	namespaceFlowData(&flowData, longID)
	if len(flowData.Nodes[0].ID) > 100 || !flowNodeIDPattern.MatchString(flowData.Nodes[0].ID) {
		t.Fatalf("invalid namespaced node ID %q", flowData.Nodes[0].ID)
	}
	first := flowData.Nodes[0].ID
	flowData.Nodes[0].ID = longID
	namespaceFlowData(&flowData, longID)
	if flowData.Nodes[0].ID != first {
		t.Fatalf("namespace is not stable: %q != %q", flowData.Nodes[0].ID, first)
	}
}
