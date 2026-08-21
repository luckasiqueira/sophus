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
		valid                                          bool
	}{
		{response: "cliente@example.com", validationType: "email", valid: true},
		{response: "cliente", validationType: "email", valid: false},
		{response: "10,5", validationType: "number", valid: true},
		{response: "ABC-123", validationType: "regex", pattern: `^[A-Z]{3}-\d{3}$`, valid: true},
		{response: "", messageType: "image", validationType: "any", valid: true},
		{response: "", messageType: "image", validationType: "text", valid: false},
		{response: "", validationType: "any", valid: false},
	}
	for _, test := range tests {
		if got := validInputResponse(test.response, test.messageType, test.validationType, test.pattern); got != test.valid {
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
