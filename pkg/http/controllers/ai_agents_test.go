package controllers

import "testing"

func TestNormalizeAIAgentRequiresCredential(t *testing.T) {
	body := aiAgentRequest{
		Name: "Support", Tone: "professional",
		MaxOutputTokens: 100, Temperature: 0.4, HistoryLimit: 20, IsActive: true,
	}
	if _, message := normalizeAIAgentRequest(&body); message != "credencial de IA é obrigatória" {
		t.Fatalf("unexpected validation result: %q", message)
	}
}

func TestNormalizeAIAgentUsesCredentialID(t *testing.T) {
	body := aiAgentRequest{
		Name: " Support ", CredentialId: 42, Tone: "professional",
		MaxOutputTokens: 100, Temperature: 0.4, HistoryLimit: 20, IsActive: true,
	}
	input, message := normalizeAIAgentRequest(&body)
	if message != "" || input.CredentialId != 42 || input.Name != "Support" || !input.SafeMode {
		t.Fatalf("valid credential rejected: message=%q input=%#v", message, input)
	}
}

func TestNormalizeAIAgentAcceptsDisabledSafeMode(t *testing.T) {
	disabled := false
	body := aiAgentRequest{
		Name: "Support", CredentialId: 42, Tone: "professional", SafeMode: &disabled,
		MaxOutputTokens: 100, Temperature: 0.4, HistoryLimit: 20, IsActive: true,
	}
	input, message := normalizeAIAgentRequest(&body)
	if message != "" || input.SafeMode {
		t.Fatalf("disabled safe mode was not preserved: message=%q input=%#v", message, input)
	}
}
