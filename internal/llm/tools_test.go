package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// sequenceProvider answers each request with the next reply and keeps every
// request body for inspection.
func sequenceProvider(t *testing.T, replies ...string) (*httptest.Server, func() []map[string]interface{}) {
	t.Helper()
	var mutex sync.Mutex
	var bodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		mutex.Lock()
		index := len(bodies)
		bodies = append(bodies, body)
		mutex.Unlock()
		if index >= len(replies) {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = response.Write([]byte(replies[index]))
	}))
	t.Cleanup(server.Close)
	return server, func() []map[string]interface{} {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]map[string]interface{}(nil), bodies...)
	}
}

var testTools = []ToolDefinition{{
	Name: "agendar_reuniao", Description: "Agenda uma reunião",
	Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"data": map[string]interface{}{"type": "string", "description": "Data desejada"},
	}, "required": []string{"data"}},
}}

func recordingExecutor(calls *[]ToolCall) ToolExecutor {
	return func(_ context.Context, call ToolCall) string {
		*calls = append(*calls, call)
		return "reunião agendada"
	}
}

func TestCompleteWithToolsOpenAIRunsCallsAndReturnsFinalText(t *testing.T) {
	server, bodies := sequenceProvider(t,
		`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"agendar_reuniao","arguments":"{\"data\":\"amanhã\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`,
		`{"choices":[{"message":{"content":"Pronto, agendado!"}}],"usage":{"prompt_tokens":20,"completion_tokens":6}}`)
	previous := openAIChatURL
	openAIChatURL = server.URL
	t.Cleanup(func() { openAIChatURL = previous })
	var calls []ToolCall
	result, err := CompleteWithTools(context.Background(), Request{Provider: "openai", Model: "gpt-4o-mini", APIKey: "k", System: "s",
		Messages: []Message{{Role: "user", Content: "quero agendar"}}, MaxOutputTokens: 100}, testTools, recordingExecutor(&calls))
	if err != nil || result.Text != "Pronto, agendado!" || result.PromptTokens != 30 || result.CompletionTokens != 10 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(calls) != 1 || calls[0].Name != "agendar_reuniao" || calls[0].Arguments["data"] != "amanhã" {
		t.Fatalf("calls=%#v", calls)
	}
	sent := bodies()
	encodedFirst, _ := json.Marshal(sent[0]["tools"])
	if !strings.Contains(string(encodedFirst), `"name":"agendar_reuniao"`) {
		t.Fatalf("tools not sent: %s", encodedFirst)
	}
	encoded, _ := json.Marshal(sent[1]["messages"])
	for _, expected := range []string{`"tool_calls"`, `"role":"tool"`, `"tool_call_id":"call_1"`, `"content":"reunião agendada"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("second request misses %s: %s", expected, encoded)
		}
	}
}

func TestCompleteWithToolsClaudeReturnsBlocksAndToolResults(t *testing.T) {
	server, bodies := sequenceProvider(t,
		`{"content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"toolu_1","name":"agendar_reuniao","input":{"data":"sexta"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":2}}`,
		`{"content":[{"type":"text","text":"Agendado para sexta."}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`)
	useClaudeEndpoint(t, server.URL)
	var calls []ToolCall
	result, err := CompleteWithTools(context.Background(), Request{Provider: "claude", Model: "claude-haiku-5-5", APIKey: "k",
		Messages: []Message{{Role: "user", Content: "agende"}}, MaxOutputTokens: 100}, testTools, recordingExecutor(&calls))
	if err != nil || result.Text != "Agendado para sexta." || result.PromptTokens != 12 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(calls) != 1 || calls[0].Arguments["data"] != "sexta" {
		t.Fatalf("calls=%#v", calls)
	}
	encoded, _ := json.Marshal(bodies()[1]["messages"])
	for _, expected := range []string{`"signature":"sig"`, `"type":"tool_result"`, `"tool_use_id":"toolu_1"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("second request misses %s: %s", expected, encoded)
		}
	}
	encodedTools, _ := json.Marshal(bodies()[0]["tools"])
	if !strings.Contains(string(encodedTools), `"input_schema"`) {
		t.Fatalf("Claude tools need input_schema: %s", encodedTools)
	}
}

func TestCompleteWithToolsGeminiKeepsPartsAndSendsFunctionResponse(t *testing.T) {
	server, bodies := sequenceProvider(t,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"agendar_reuniao","args":{"data":"hoje"}},"thoughtSignature":"abc"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"pensando","thought":true},{"text":"Feito!"}]}}]}`)
	useEndpoints(t, server.URL, "")
	var calls []ToolCall
	result, err := CompleteWithTools(context.Background(), Request{Provider: "gemini", Model: "gemini-3.8-flash", APIKey: "k",
		Messages: []Message{{Role: "user", Content: "agende"}}, MaxOutputTokens: 100}, testTools, recordingExecutor(&calls))
	if err != nil || result.Text != "Feito!" || len(calls) != 1 || calls[0].Arguments["data"] != "hoje" {
		t.Fatalf("result=%#v calls=%#v err=%v", result, calls, err)
	}
	encoded, _ := json.Marshal(bodies()[1]["contents"])
	for _, expected := range []string{`"thoughtSignature":"abc"`, `"functionResponse"`, `"result":"reunião agendada"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("second request misses %s: %s", expected, encoded)
		}
	}
}

func TestCompleteWithToolsForbidsToolsOnLastStep(t *testing.T) {
	call := `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"agendar_reuniao","arguments":"{}"}}]}}]}`
	replies := make([]string, 0, MaxToolSteps)
	for len(replies) < MaxToolSteps-1 {
		replies = append(replies, call)
	}
	replies = append(replies, `{"choices":[{"message":{"content":"Encerrando."}}]}`)
	server, bodies := sequenceProvider(t, replies...)
	previous := deepseekChatURL
	deepseekChatURL = server.URL
	t.Cleanup(func() { deepseekChatURL = previous })
	var calls []ToolCall
	result, err := CompleteWithTools(context.Background(), Request{Provider: "deepseek", Model: "deepseek-flash", APIKey: "k",
		Messages: []Message{{Role: "user", Content: "oi"}}, MaxOutputTokens: 100}, testTools, recordingExecutor(&calls))
	if err != nil || result.Text != "Encerrando." || len(calls) != MaxToolSteps-1 {
		t.Fatalf("result=%#v calls=%d err=%v", result, len(calls), err)
	}
	sent := bodies()
	if sent[len(sent)-1]["tool_choice"] != "none" || sent[0]["tool_choice"] != nil {
		t.Fatalf("tool_choice first=%v last=%v", sent[0]["tool_choice"], sent[len(sent)-1]["tool_choice"])
	}
}
