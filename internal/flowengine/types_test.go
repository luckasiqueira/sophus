package flowengine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateFlowDataRequiresAudio(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"audio","type":"sendAudio","data":{}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected sendAudio without media to be rejected")
	}
}

func TestValidateFlowDataAcceptsUploadedImage(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"image","type":"sendImage","data":{"imagePath":"image.png"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected uploaded image to be accepted: %v", err)
	}
}

func TestValidateFlowDataAcceptsUploadedDocument(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"document","type":"sendFile","data":{"filePath":"contract.pdf"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected uploaded document to be accepted: %v", err)
	}
}

func TestValidateFlowDataRejectsUnsafeHTTPConfiguration(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "arbitrary method", data: `{"method":"CONNECT","url":"https://example.com"}`},
		{name: "unsafe header", data: `{"method":"GET","url":"https://example.com","headerMode":"fields","headerFields":[{"key":"X-Forwarded-For","value":"127.0.0.1"}]}`},
		{name: "unbounded timeout", data: `{"method":"GET","url":"https://example.com","timeout":240001}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"nodes":[{"id":"http","type":"httpRequest","data":` + test.data + `}],"edges":[]}`)
			if err := ValidateFlowData(raw); err == nil {
				t.Fatal("expected unsafe HTTP configuration to be rejected")
			}
		})
	}
}

func TestValidateFlowDataAcceptsTerminalAIAgent(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"agent","type":"aiAgent","data":{"aiAgentId":7}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected terminal AI agent node to be accepted: %v", err)
	}
}

func TestValidateFlowDataRequiresAICredential(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"ai","type":"ai","data":{"prompt":"Olá","saveResponseTo":"answer"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected AI node without credential to be rejected")
	}
	raw = json.RawMessage(`{"nodes":[{"id":"ai","type":"ai","data":{"credentialId":7,"prompt":"Olá","saveResponseTo":"answer"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected AI node with credential to be accepted: %v", err)
	}
}

func TestValidateFlowDataRequiresCredentialsForAIUtilityNodes(t *testing.T) {
	for _, nodeType := range []string{"conversationSummary", "mediaTranscription"} {
		data := `{"saveResponseTo":"result"}`
		if nodeType == "conversationSummary" {
			data = `{"maxMessages":30,"saveResponseTo":"result"}`
		}
		raw := json.RawMessage(`{"nodes":[{"id":"utility","type":"` + nodeType + `","data":` + data + `}],"edges":[]}`)
		if err := ValidateFlowData(raw); err == nil {
			t.Fatalf("expected %s without credential to be rejected", nodeType)
		}

		data = `{"credentialId":7,"saveResponseTo":"result"}`
		if nodeType == "conversationSummary" {
			data = `{"credentialId":7,"maxMessages":30,"saveResponseTo":"result"}`
		}
		raw = json.RawMessage(`{"nodes":[{"id":"utility","type":"` + nodeType + `","data":` + data + `}],"edges":[]}`)
		if err := ValidateFlowData(raw); err != nil {
			t.Fatalf("expected %s with credential to be accepted: %v", nodeType, err)
		}
	}
}

func TestValidateFlowDataAcceptsLegacyAIAgentForUtilityNodes(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[
		{"id":"summary","type":"conversationSummary","data":{"aiAgentId":7,"maxMessages":30,"saveResponseTo":"summary"}},
		{"id":"media","type":"mediaTranscription","data":{"aiAgentId":7,"saveResponseTo":"media"}}
	],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected legacy AI-agent references to remain valid: %v", err)
	}
}

func TestAISafeModeDefaultsToEnabledAndCanBeDisabled(t *testing.T) {
	if !safeModeEnabled(map[string]interface{}{}) {
		t.Fatal("legacy AI nodes should default to safe mode")
	}
	if safeModeEnabled(map[string]interface{}{"safeMode": false}) {
		t.Fatal("explicitly disabled safe mode was ignored")
	}
	if !safeModeEnabled(map[string]interface{}{"safeMode": true}) {
		t.Fatal("enabled safe mode was ignored")
	}
}

func TestValidateFlowDataRejectsInvalidAISafeMode(t *testing.T) {
	tests := []string{
		`{"id":"ai","type":"ai","data":{"credentialId":7,"prompt":"Olá","saveResponseTo":"answer","safeMode":"yes"}}`,
		`{"id":"agent","type":"aiAgent","data":{"aiAgentId":7,"safeMode":"yes"}}`,
		`{"id":"summary","type":"conversationSummary","data":{"credentialId":7,"maxMessages":30,"saveResponseTo":"summary","safeMode":"yes"}}`,
		`{"id":"media","type":"mediaTranscription","data":{"credentialId":7,"saveResponseTo":"media","safeMode":"yes"}}`,
	}
	for _, node := range tests {
		raw := json.RawMessage(`{"nodes":[` + node + `],"edges":[]}`)
		if err := ValidateFlowData(raw); err == nil || !strings.Contains(err.Error(), "modo seguro") {
			t.Fatalf("expected invalid safe mode to be rejected: node=%s error=%v", node, err)
		}
	}
}

func TestValidateFlowDataLimitsMediaTranscriptionPrompt(t *testing.T) {
	prompt := strings.Repeat("a", 2001)
	raw := json.RawMessage(`{"nodes":[{"id":"media","type":"mediaTranscription","data":{"credentialId":7,"prompt":"` + prompt + `","saveResponseTo":"media"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected oversized media-transcription prompt to be rejected")
	}
}

func TestValidateFlowDataLimitsAIKnowledgeBase(t *testing.T) {
	knowledge := strings.Repeat("a", 500001)
	raw := json.RawMessage(`{"nodes":[{"id":"ai","type":"ai","data":{"credentialId":7,"prompt":"Olá","knowledgeBase":"` + knowledge + `"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected oversized AI knowledge base to be rejected")
	}
}

func TestValidateFlowDataRejectsEdgeAfterAIAgent(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"agent","type":"aiAgent","data":{"aiAgentId":7}},{"id":"end","type":"end","data":{}}],"edges":[{"id":"edge","source":"agent","target":"end"}]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected an outgoing edge from the terminal AI agent node to be rejected")
	}
}

func TestValidateFlowDataAcceptsNoResponseSettings(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[],"edges":[],"settings":{"noResponse":{"enabled":true,"reminderMinutes":15,"reminderMessage":"Ainda precisa de ajuda?","targetType":"department","targetId":7}}}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected no-response settings to be accepted: %v", err)
	}
	parsed, err := ParseFlowData(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Settings.NoResponse.TargetID != 7 || parsed.Settings.NoResponse.ReminderMinutes != 15 {
		t.Fatalf("no-response settings were not preserved: %#v", parsed.Settings.NoResponse)
	}
}

func TestValidateFlowDataRejectsInvalidNoResponseSettings(t *testing.T) {
	tests := []string{
		`{"enabled":true,"reminderMinutes":0,"reminderMessage":"Lembrete","targetType":"agent","targetId":1}`,
		`{"enabled":true,"reminderMinutes":10,"reminderMessage":"","targetType":"agent","targetId":1}`,
		`{"enabled":true,"reminderMinutes":10,"reminderMessage":"Lembrete","targetType":"queue","targetId":1}`,
		`{"enabled":true,"reminderMinutes":10,"reminderMessage":"Lembrete","targetType":"department","targetId":0}`,
	}
	for _, settings := range tests {
		raw := json.RawMessage(`{"nodes":[],"edges":[],"settings":{"noResponse":` + settings + `}}`)
		if err := ValidateFlowData(raw); err == nil {
			t.Fatalf("expected invalid no-response settings to be rejected: %s", settings)
		}
	}
}

func TestApplyNoResponseWaitOverridesNodeTimeout(t *testing.T) {
	ctx := ExecutionContext{}
	flowData := FlowData{Settings: FlowSettings{NoResponse: NoResponseSettings{Enabled: true, ReminderMinutes: 8}}}
	if got := applyNoResponseWait(flowData, "input-1", ctx, 30); got != 8*60 {
		t.Fatalf("applyNoResponseWait() = %d, want 480", got)
	}
	if ctx[noResponseWaitNodeKey] != "input-1" {
		t.Fatalf("wait node marker = %#v", ctx[noResponseWaitNodeKey])
	}
	disabledContext := ExecutionContext{}
	if got := applyNoResponseWait(FlowData{}, "input-1", disabledContext, 30); got != 30 {
		t.Fatalf("disabled applyNoResponseWait() = %d, want 30", got)
	}
	if _, exists := disabledContext[noResponseWaitNodeKey]; exists {
		t.Fatal("disabled no-response settings marked the waiting context")
	}
}
