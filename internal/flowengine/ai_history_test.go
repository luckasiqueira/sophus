package flowengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sophus/internal/llm"
	"sophus/internal/repo"
	"sophus/utils/env"
)

var testPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

func TestBuildAIMessagesDescribesMediaAndAttachesImages(t *testing.T) {
	history := []repo.AIHistoryMessage{
		{Role: "user", Content: "Olá", MediaType: "text"},
		{Role: "assistant", Content: "Catálogo", MediaType: "document"},
		{Role: "user", MediaType: "audio"},
		{Role: "user", Content: "Esse modelo", MediaType: "image"},
		{Role: "user", MediaType: "image"},
	}
	image := llm.Image{MIME: "image/png", Data: testPNG}
	current := aiAgentInput("", "image")
	messages := buildAIMessages(history, current, map[int]llm.Image{4: image})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Content != "[A empresa enviou um documento] Catálogo" {
		t.Fatalf("company attachment = %q", messages[1].Content)
	}
	last := messages[2]
	// The image not attached keeps its caption with a note; the attached one
	// goes as a picture; the current message is not repeated.
	for _, expected := range []string{"[O cliente enviou um áudio que não foi transcrito.]", "[O cliente enviou uma imagem] Esse modelo", current} {
		if !strings.Contains(last.Content, expected) {
			t.Fatalf("missing %q in %q", expected, last.Content)
		}
	}
	if strings.Count(last.Content, current) != 1 || len(last.Images) != 1 {
		t.Fatalf("current message repeated or image missing: %#v", last)
	}
}

func TestBuildAIMessagesAppendsCurrentMessageWhenNotStored(t *testing.T) {
	messages := buildAIMessages([]repo.AIHistoryMessage{{Role: "user", Content: "oi"}, {Role: "assistant", Content: "olá"}}, "preço?", nil)
	if len(messages) != 3 || messages[2].Content != "preço?" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestLoadAIHistoryImagesRespectsLimitAndModel(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	root := t.TempDir()
	env.Backend["MEDIA_DIRECTORY"] = root
	defer func() { env.Backend["MEDIA_DIRECTORY"] = original }()
	directory := filepath.Join(root, "5")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		if err := os.WriteFile(filepath.Join(directory, name), testPNG, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "fake.png"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	history := []repo.AIHistoryMessage{
		{Role: "user", MediaType: "image", MediaPath: "a.png"},
		{Role: "assistant", MediaType: "image", MediaPath: "b.png"},
		{Role: "user", MediaType: "image", MediaPath: "c.png"},
		{Role: "user", MediaType: "image", MediaPath: "fake.png"},
		{Role: "user", MediaType: "image", MediaPath: "../escape.png"},
	}
	agent := repo.AIAgent{Provider: "openai", Model: "gpt-4o-mini", HistoryImageLimit: 1}
	images := loadAIHistoryImages(history, 5, agent, "")
	if len(images) != 1 || images[2].MIME != "image/png" {
		t.Fatalf("expected only the newest valid customer image, got %v", images)
	}
	agent.HistoryImageLimit = 10
	if images = loadAIHistoryImages(history, 5, agent, ""); len(images) != 2 || images[1].Data != nil {
		t.Fatalf("company images and invalid files must be skipped, got %v", images)
	}
	for _, blind := range []repo.AIAgent{{Provider: "deepseek", Model: "deepseek-flash", HistoryImageLimit: 3}, {Provider: "groq", Model: "openai/gpt-oss-20b", HistoryImageLimit: 3}} {
		if images := loadAIHistoryImages(history, 5, blind, ""); len(images) != 0 {
			t.Fatalf("%s/%s cannot see images, got %v", blind.Provider, blind.Model, images)
		}
	}
}
