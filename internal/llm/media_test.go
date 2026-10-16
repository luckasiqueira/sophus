package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func captureProvider(t *testing.T, status int, reply string) (*httptest.Server, chan *http.Request, chan map[string]interface{}) {
	t.Helper()
	requests := make(chan *http.Request, 1)
	bodies := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		requests <- request
		bodies <- body
		response.WriteHeader(status)
		_, _ = response.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server, requests, bodies
}

func useEndpoints(t *testing.T, gemini, groq string) {
	t.Helper()
	previousGemini, previousGroq := geminiAPIBase, groqChatURL
	geminiAPIBase, groqChatURL = gemini, groq
	t.Cleanup(func() { geminiAPIBase, groqChatURL = previousGemini, previousGroq })
}

func useClaudeEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	previous := claudeURL
	claudeURL = endpoint
	t.Cleanup(func() { claudeURL = previous })
}

func TestAnalyzeMediaWithGeminiSendsInlineData(t *testing.T) {
	server, requests, bodies := captureProvider(t, http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"Descrição de teste da imagem"}]}}]}`)
	useEndpoints(t, server.URL, "")
	image := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	result, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "gemini-key", "image", "image/png", image, "Instrução de teste", true)
	if err != nil {
		t.Fatalf("AnalyzeMedia: %v", err)
	}
	if result.Text != "Descrição de teste da imagem" {
		t.Fatalf("text = %q", result.Text)
	}
	request, body := <-requests, <-bodies
	if request.URL.Path != "/models/gemini-3.8-flash:generateContent" || request.URL.RawQuery != "" || request.Header.Get("x-goog-api-key") != "gemini-key" {
		t.Fatalf("unexpected endpoint %s or key header %q", request.URL.String(), request.Header.Get("x-goog-api-key"))
	}
	encoded, _ := json.Marshal(body)
	for _, expected := range []string{`"mimeType":"image/png"`, `"data":"` + base64.StdEncoding.EncodeToString(image) + `"`, `"text":"Instrução de teste"`, `"systemInstruction"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("payload %s lacks %s", encoded, expected)
		}
	}
}

func TestAnalyzeMediaWithGeminiHandlesAudioDocumentsAndFailures(t *testing.T) {
	server, _, bodies := captureProvider(t, http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"transcrição de teste"}]}}]}`)
	useEndpoints(t, server.URL, "")
	if _, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "k", "audio", "audio/ogg; codecs=opus", []byte("ogg"), "", false); err != nil {
		t.Fatalf("audio: %v", err)
	}
	encoded, _ := json.Marshal(<-bodies)
	if !strings.Contains(string(encoded), `"mimeType":"audio/ogg"`) || !strings.Contains(string(encoded), defaultAudioPrompt) || strings.Contains(string(encoded), "systemInstruction") {
		t.Fatalf("audio payload %s", encoded)
	}
	if _, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "k", "document", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte("docx"), "", true); err == nil || MediaErrorMessage(err) != "o Gemini lê somente documentos PDF ou de texto" {
		t.Fatalf("docx accepted or wrong reason: %v", err)
	}
	large := make([]byte, geminiInlineMaxBytes+1)
	if _, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "k", "video", "video/mp4", large, "", true); err == nil || !strings.Contains(MediaErrorMessage(err), "até 14 MB") {
		t.Fatalf("oversized video: %v", err)
	}

	limited, _, _ := captureProvider(t, http.StatusTooManyRequests, `{"error":{"message":"quota exceeded for key secret"}}`)
	useEndpoints(t, limited.URL, "")
	_, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "k", "image", "image/jpeg", []byte("jpg"), "", true)
	if reason := MediaErrorMessage(err); reason != "o limite de uso do provedor foi atingido" || strings.Contains(reason, "secret") {
		t.Fatalf("rate limit reason = %q", reason)
	}

	blocked, _, _ := captureProvider(t, http.StatusOK, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	useEndpoints(t, blocked.URL, "")
	if _, err := AnalyzeMedia(context.Background(), "gemini", "gemini-3.8-flash", "k", "image", "image/jpeg", []byte("jpg"), "", true); err == nil || !strings.Contains(MediaErrorMessage(err), "políticas de segurança") {
		t.Fatalf("blocked media: %v", err)
	}
}

func TestAnalyzeImageWithGroqVision(t *testing.T) {
	server, requests, bodies := captureProvider(t, http.StatusOK, `{"choices":[{"message":{"content":"Descrição de teste da foto"}}]}`)
	useEndpoints(t, "", server.URL)
	result, err := AnalyzeMedia(context.Background(), "groq", "openai/gpt-oss-20b", "groq-key", "image", "image/jpeg", []byte("jpg"), "", true)
	if err != nil || result.Text != "Descrição de teste da foto" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	request, body := <-requests, <-bodies
	if request.Header.Get("Authorization") != "Bearer groq-key" {
		t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
	}
	encoded, _ := json.Marshal(body)
	for _, expected := range []string{`"model":"` + groqVisionModel + `"`, `"url":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString([]byte("jpg")) + `"`, `"role":"system"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("payload %s lacks %s", encoded, expected)
		}
	}
	if _, err := AnalyzeMedia(context.Background(), "groq", "openai/gpt-oss-20b", "k", "image", "image/png", make([]byte, groqImageMaxBytes+1), "", true); err == nil || !strings.Contains(MediaErrorMessage(err), "até 3 MB") {
		t.Fatalf("oversized Groq image: %v", err)
	}
	if _, err := AnalyzeMedia(context.Background(), "groq", "openai/gpt-oss-20b", "k", "video", "video/mp4", []byte("mp4"), "", true); err == nil || MediaErrorMessage(err) != "a credencial Groq analisa somente áudio e imagem, não vídeo" {
		t.Fatalf("Groq video: %v", err)
	}
	if MediaAnalysisProvider("deepseek") || !MediaAnalysisProvider("gemini") || UnsupportedMediaReason("deepseek", "") == "" {
		t.Fatal("unexpected provider support")
	}
}

func TestGeminiChatSendsKeyInHeader(t *testing.T) {
	server, requests, _ := captureProvider(t, http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"configuração validada"}]}}]}`)
	useEndpoints(t, server.URL, "")
	result, err := Complete(context.Background(), Request{Provider: "gemini", Model: "gemini-3.5-flash-lite", APIKey: "AQ.test-key",
		System: "Responda curto.", Messages: []Message{{Role: "user", Content: "teste"}}, MaxOutputTokens: 50})
	if err != nil || result.Text != "configuração validada" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	request := <-requests
	if request.Header.Get("x-goog-api-key") != "AQ.test-key" || strings.Contains(request.URL.String(), "AQ.test-key") || request.Header.Get("Authorization") != "" {
		t.Fatalf("key not sent only in x-goog-api-key: url=%s auth=%q", request.URL.String(), request.Header.Get("Authorization"))
	}
	notFound, _, _ := captureProvider(t, http.StatusNotFound, `{"error":{"message":"models/gemini-2.5-flash is no longer available to new users"}}`)
	useEndpoints(t, notFound.URL, "")
	_, err = Complete(context.Background(), Request{Provider: "gemini", Model: "gemini-2.5-flash", APIKey: "k", Messages: []Message{{Role: "user", Content: "teste"}}})
	if failure, ok := FailureForError(err); !ok || failure.Category != "model_not_found" || failure.Provider != "gemini" {
		t.Fatalf("404 not classified as model_not_found: %#v %v", failure, err)
	}
}

func TestAnalyzeMediaWithClaudeImagesAndDocuments(t *testing.T) {
	server, requests, bodies := captureProvider(t, http.StatusOK, `{"content":[{"type":"text","text":"Descrição de teste da imagem"}]}`)
	useClaudeEndpoint(t, server.URL)
	image := []byte("jpg-bytes")
	result, err := AnalyzeMedia(context.Background(), "claude", "claude-haiku-4-5-20251001", "claude-key", "image", "image/jpeg", image, "Instrução de teste", true)
	if err != nil || result.Text != "Descrição de teste da imagem" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	request, body := <-requests, <-bodies
	if request.Header.Get("x-api-key") != "claude-key" || request.Header.Get("anthropic-version") == "" {
		t.Fatalf("Claude auth headers missing: %v", request.Header)
	}
	encoded, _ := json.Marshal(body)
	for _, expected := range []string{`"type":"image"`, `"media_type":"image/jpeg"`, `"data":"` + base64.StdEncoding.EncodeToString(image) + `"`, `"system"`, `"text":"Instrução de teste"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("payload %s lacks %s", encoded, expected)
		}
	}
	if _, err := AnalyzeMedia(context.Background(), "claude", "claude-haiku-4-5-20251001", "k", "document", "application/pdf", []byte("%PDF"), "", false); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	encoded, _ = json.Marshal(<-bodies)
	if !strings.Contains(string(encoded), `"type":"document"`) || !strings.Contains(string(encoded), `"media_type":"application/pdf"`) || strings.Contains(string(encoded), `"system"`) {
		t.Fatalf("pdf payload %s", encoded)
	}
	<-requests
	for name, test := range map[string]struct {
		kind, mime string
		size       int
		reason     string
	}{
		"audio":          {"audio", "audio/ogg", 10, "não áudio"},
		"video":          {"video", "video/mp4", 10, "não vídeo"},
		"word document":  {"document", "application/msword", 10, "PDF ou de texto"},
		"heic image":     {"image", "image/heic", 10, "JPEG, PNG, GIF ou WebP"},
		"oversized jpeg": {"image", "image/jpeg", claudeImageMaxBytes + 1, "até 5 MB"},
	} {
		_, err := AnalyzeMedia(context.Background(), "claude", "claude-haiku-4-5-20251001", "k", test.kind, test.mime, make([]byte, test.size), "", true)
		if err == nil || !strings.Contains(MediaErrorMessage(err), test.reason) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
