package controllers

import (
	"testing"
	"time"

	"sophus/internal/llm"
	"sophus/utils/env"
)

func TestAICredentialTestTimeoutStaysBelowProviderClientTimeout(t *testing.T) {
	if aiCredentialTestTimeout <= 0 || aiCredentialTestTimeout >= 75*time.Second {
		t.Fatalf("aiCredentialTestTimeout = %s, want a positive timeout below the provider client timeout", aiCredentialTestTimeout)
	}
}

func TestNormalizeAICredentialValidatesCatalog(t *testing.T) {
	body := aiCredentialRequest{Name: "Primary", Provider: "openai", Model: "custom", APIKey: "secret"}
	if _, message := normalizeAICredentialRequest(&body, true); message != "modelo não permitido para o provedor selecionado" {
		t.Fatalf("unexpected validation result: %q", message)
	}
}

func TestNormalizeAICredentialEncryptsCreateAndPreservesBlankUpdate(t *testing.T) {
	original := env.Backend["AI_CREDENTIALS_KEY"]
	env.Backend["AI_CREDENTIALS_KEY"] = "controller-test-master-key"
	t.Cleanup(func() { env.Backend["AI_CREDENTIALS_KEY"] = original })

	create := aiCredentialRequest{Name: " Primary ", Provider: " OPENAI ", Model: "gpt-4o-mini", APIKey: " secret "}
	input, message := normalizeAICredentialRequest(&create, true)
	if message != "" || input.Name != "Primary" || input.Provider != "openai" || input.APIKeyCiphertext == "" {
		t.Fatalf("valid credential rejected: message=%q input=%#v", message, input)
	}
	plain, err := llm.DecryptAPIKey(input.APIKeyCiphertext)
	if err != nil || plain != "secret" {
		t.Fatalf("API key was not encrypted correctly: plain=%q err=%v", plain, err)
	}

	update := aiCredentialRequest{Name: "Primary", Provider: "openai", Model: "gpt-4o-mini", Revision: 2}
	input, message = normalizeAICredentialRequest(&update, false)
	if message != "" || input.APIKeyCiphertext != "" || input.Revision != 2 {
		t.Fatalf("blank update key was not preserved: message=%q input=%#v", message, input)
	}
}
