package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureChatProvider answers every request with reply and keeps the last body.
func captureChatProvider(t *testing.T, reply string, body *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		*body = string(data)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server
}

func imageRequest(provider, model string) Request {
	return Request{Provider: provider, Model: model, APIKey: "k", MaxOutputTokens: 50, Messages: []Message{
		{Role: "user", Content: "Olha esta foto", Images: []Image{{MIME: "image/png", Data: []byte("png-bytes")}}},
	}}
}

func TestChatImagesReachEachProvider(t *testing.T) {
	const encoded = "cG5nLWJ5dGVz" // base64 of "png-bytes"
	var body string
	openAI := captureChatProvider(t, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, &body)
	previousOpenAI := openAIChatURL
	openAIChatURL = openAI.URL
	defer func() { openAIChatURL = previousOpenAI }()
	if _, err := Complete(context.Background(), imageRequest("openai", "gpt-4o-mini")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"image_url":{"url":"data:image/png;base64,`+encoded+`"}`) || !strings.Contains(body, `{"text":"Olha esta foto","type":"text"}`) {
		t.Fatalf("OpenAI payload: %s", body)
	}

	claude := captureChatProvider(t, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`, &body)
	previousClaude := claudeURL
	claudeURL = claude.URL
	defer func() { claudeURL = previousClaude }()
	if _, err := Complete(context.Background(), imageRequest("claude", "claude-haiku-5-5")); err != nil {
		t.Fatal(err)
	}
	var claudePayload struct {
		Messages []struct {
			Content []map[string]interface{} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &claudePayload); err != nil || len(claudePayload.Messages[0].Content) != 2 || claudePayload.Messages[0].Content[0]["type"] != "image" {
		t.Fatalf("Claude payload: %s", body)
	}

	gemini := captureChatProvider(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`, &body)
	previousGemini := geminiAPIBase
	geminiAPIBase = gemini.URL
	defer func() { geminiAPIBase = previousGemini }()
	if _, err := Complete(context.Background(), imageRequest("gemini", "gemini-3.8-flash")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"inlineData":{"data":"`+encoded+`","mimeType":"image/png"}`) {
		t.Fatalf("Gemini payload: %s", body)
	}
}

func TestTextOnlyMessagesKeepPlainContent(t *testing.T) {
	encoded, _ := json.Marshal(openAIMessages("sistema", []Message{{Role: "user", Content: "oi"}}))
	if string(encoded) != `[{"content":"sistema","role":"system"},{"content":"oi","role":"user"}]` {
		t.Fatalf("text-only messages must stay plain strings: %s", encoded)
	}
}

func TestSupportsChatImages(t *testing.T) {
	cases := map[[2]string]bool{
		{"openai", "gpt-4o-mini"}: true, {"claude", "claude-haiku-5-5"}: true, {"gemini", "gemini-3.8-flash"}: true,
		{"groq", groqVisionModel}: true, {"groq", "openai/gpt-oss-20b"}: false, {"deepseek", "deepseek-flash"}: false,
	}
	for input, expected := range cases {
		if SupportsChatImages(input[0], input[1]) != expected {
			t.Fatalf("SupportsChatImages(%v) != %v", input, expected)
		}
	}
}
