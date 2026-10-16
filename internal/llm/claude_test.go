package llm

import (
	"context"
	"net/http"
	"testing"
)

func TestClaudeHaiku55PayloadOmitsTemperatureAndUsesLowEffort(t *testing.T) {
	if !IsSupportedModel("claude", "claude-haiku-5-5") || ProviderModels()["claude"][0].ID != "claude-haiku-5-5" {
		t.Fatal("Claude Haiku 5.5 must be the first Claude option")
	}
	server, requests, bodies := captureProvider(t, http.StatusOK, `{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"Olá!"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":5}}`)
	useClaudeEndpoint(t, server.URL)
	result, err := Complete(context.Background(), Request{Provider: "claude", Model: "claude-haiku-5-5", APIKey: "k", System: "Seja breve.", Messages: []Message{{Role: "user", Content: "oi"}}, MaxOutputTokens: 1024, Temperature: 0.7})
	if err != nil || result.Text != "Olá!" || result.PromptTokens != 12 || result.CompletionTokens != 5 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	request, body := <-requests, <-bodies
	if request.Header.Get("anthropic-version") != "2023-06-01" || request.Header.Get("x-api-key") != "k" {
		t.Fatalf("Claude headers missing: %v", request.Header)
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("Claude Haiku 5.5 rejects temperature, payload sent it: %v", body)
	}
	if config, _ := body["output_config"].(map[string]interface{}); config["effort"] != "low" {
		t.Fatalf("Claude Haiku 5.5 must run at low effort: %v", body)
	}
	if body["model"] != "claude-haiku-5-5" || body["system"] != "Seja breve." || body["max_tokens"] != float64(1024) {
		t.Fatalf("unexpected payload: %v", body)
	}
}

func TestClaudeTemperatureOnlyForModelsThatAcceptIt(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-haiku-4-5-20251001": true,
		"claude-haiku-5-5":          false,
		"claude-sonnet-5":           false,
		"claude-opus-5":             false,
	} {
		if got := claudeAcceptsTemperature(model); got != want {
			t.Fatalf("claudeAcceptsTemperature(%q) = %v, want %v", model, got, want)
		}
		if _, sent := claudePayload(model, 100, 0.4, "", nil)["temperature"]; sent != want {
			t.Fatalf("%s temperature sent = %v, want %v", model, sent, want)
		}
	}
	if _, ok := claudePayload("claude-haiku-4-5-20251001", 100, 0.4, "", nil)["output_config"]; ok {
		t.Fatal("effort must only be set for Claude Haiku 5.5")
	}
}

func TestClaudeEmptyResponsesExplainRefusalAndTokenLimit(t *testing.T) {
	for stopReason, category := range map[string]string{"refusal": "refusal", "max_tokens": "max_tokens"} {
		server, _, _ := captureProvider(t, http.StatusOK, `{"content":[{"type":"thinking","thinking":""}],"stop_reason":"`+stopReason+`"}`)
		useClaudeEndpoint(t, server.URL)
		_, err := Complete(context.Background(), Request{Provider: "claude", Model: "claude-haiku-5-5", APIKey: "k", Messages: []Message{{Role: "user", Content: "oi"}}, MaxOutputTokens: 64})
		if failure, ok := FailureForError(err); !ok || failure.Category != category || failure.Retryable {
			t.Fatalf("%s: failure=%#v err=%v", stopReason, failure, err)
		}
		if SafeErrorMessage(err) == "o provedor de IA está indisponível no momento" {
			t.Fatalf("%s needs its own message", stopReason)
		}
	}
}
