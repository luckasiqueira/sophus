package llm

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sophus/utils/env"
)

func TestAPIKeyEncryptionRoundTrip(t *testing.T) {
	original := env.Backend["AI_CREDENTIALS_KEY"]
	env.Backend["AI_CREDENTIALS_KEY"] = "test-master-key-that-is-not-used-in-production"
	t.Cleanup(func() { env.Backend["AI_CREDENTIALS_KEY"] = original })

	ciphertext, err := EncryptAPIKey("provider-secret")
	if err != nil {
		t.Fatalf("encrypt API key: %v", err)
	}
	if ciphertext == "provider-secret" {
		t.Fatal("API key was stored as plaintext")
	}
	plaintext, err := DecryptAPIKey(ciphertext)
	if err != nil {
		t.Fatalf("decrypt API key: %v", err)
	}
	if plaintext != "provider-secret" {
		t.Fatalf("unexpected decrypted API key: %q", plaintext)
	}
}

func TestProviderFailureRedactsUpstreamResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"error":{"message":"api_key=secret-value"}}`))
	}))
	defer server.Close()

	err := doJSON(context.Background(), server.Client(), "groq", server.URL, "key", "", map[string]string{"test": "value"}, &struct{}{})
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("provider error exposes upstream response: %q", err)
	}
	failure, ok := FailureForError(err)
	if !ok || failure.Provider != "groq" || failure.StatusCode != http.StatusUnauthorized || failure.Category != "authentication" {
		t.Fatalf("unexpected provider failure: %#v, known=%t", failure, ok)
	}
	if got := SafeErrorMessage(err); got != "a chave de API foi recusada pelo provedor" {
		t.Fatalf("SafeErrorMessage() = %q", got)
	}
}

func TestProviderFailureClassifiesModelAndCreditResponses(t *testing.T) {
	for _, test := range []struct {
		status   int
		category string
		message  string
	}{
		{status: http.StatusNotFound, category: "model_not_found", message: "o modelo não está disponível para esta credencial"},
		{status: http.StatusPaymentRequired, category: "credit_required", message: "a conta do provedor não possui saldo disponível"},
	} {
		t.Run(test.category, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.WriteHeader(test.status)
			}))
			defer server.Close()

			err := doJSON(context.Background(), server.Client(), "groq", server.URL, "key", "", map[string]string{"test": "value"}, &struct{}{})
			failure, ok := FailureForError(err)
			if !ok || failure.Category != test.category {
				t.Fatalf("unexpected provider failure: %#v, known=%t", failure, ok)
			}
			if got := SafeErrorMessage(err); got != test.message {
				t.Fatalf("SafeErrorMessage() = %q, want %q", got, test.message)
			}
		})
	}
}

func TestAPIKeyEncryptionRejectsTampering(t *testing.T) {
	original := env.Backend["AI_CREDENTIALS_KEY"]
	env.Backend["AI_CREDENTIALS_KEY"] = "test-master-key-that-is-not-used-in-production"
	t.Cleanup(func() { env.Backend["AI_CREDENTIALS_KEY"] = original })

	ciphertext, err := EncryptAPIKey("provider-secret")
	if err != nil {
		t.Fatalf("encrypt API key: %v", err)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := DecryptAPIKey(base64.RawURLEncoding.EncodeToString(sealed)); err == nil {
		t.Fatal("expected tampered ciphertext to be rejected")
	}
}

func TestMediaAnalysisCompatibility(t *testing.T) {
	tests := []struct {
		provider string
		kind     string
		want     bool
	}{
		{provider: "openai", kind: "audio", want: true},
		{provider: "openai", kind: "image", want: true},
		{provider: "openai", kind: "document", want: true},
		{provider: "groq", kind: "audio", want: true},
		{provider: "groq", kind: "image", want: false},
		{provider: "gemini", kind: "audio", want: false},
	}
	for _, test := range tests {
		if got := SupportsMediaAnalysis(test.provider, test.kind); got != test.want {
			t.Fatalf("SupportsMediaAnalysis(%q, %q) = %t, want %t", test.provider, test.kind, got, test.want)
		}
	}
}

func TestAudioTranscriptionConfig(t *testing.T) {
	endpoint, model := audioTranscriptionConfig("groq", "openai/gpt-oss-20b")
	if endpoint != "https://api.groq.com/openai/v1/audio/transcriptions" || model != "whisper-large-v3-turbo" {
		t.Fatalf("unexpected Groq transcription config: endpoint=%q model=%q", endpoint, model)
	}

	endpoint, model = audioTranscriptionConfig("openai", "gpt-4o-mini")
	if endpoint != "https://api.openai.com/v1/audio/transcriptions" || model != "whisper-1" {
		t.Fatalf("unexpected OpenAI transcription config: endpoint=%q model=%q", endpoint, model)
	}
}

func TestTranscribeAudioIncludesPrompt(t *testing.T) {
	received := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := request.FormFile("file")
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		file.Close()
		received <- map[string]string{
			"model":  request.FormValue("model"),
			"prompt": request.FormValue("prompt"),
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"text":"transcrição concluída"}`))
	}))
	defer server.Close()

	result, err := transcribeAudio(context.Background(), server.Client(), "groq", server.URL,
		"whisper-large-v3-turbo", "secret", "audio/ogg", []byte("audio"), "Use termos jurídicos")
	if err != nil {
		t.Fatalf("transcribeAudio returned error: %v", err)
	}
	fields := <-received
	if fields["model"] != "whisper-large-v3-turbo" || fields["prompt"] != "Use termos jurídicos" {
		t.Fatalf("unexpected multipart fields: %#v", fields)
	}
	if result.Text != "transcrição concluída" {
		t.Fatalf("unexpected transcription: %q", result.Text)
	}
}
