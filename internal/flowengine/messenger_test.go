package flowengine

import (
	"testing"

	"sophus/internal/repo"
)

func TestBuildMediaPayloadIncludesEvolutionURL(t *testing.T) {
	mediaURL := "https://example.com/medias/1/flows/image.png?token=signed"
	payload := buildMediaPayload("5511999999999", "image", mediaURL, "Legenda", "")

	if payload["url"] != mediaURL {
		t.Fatalf("url = %#v, want %q", payload["url"], mediaURL)
	}
	if payload["type"] != "image" {
		t.Fatalf("type = %#v, want image", payload["type"])
	}
	if payload["delay"] != repo.TypingDelayMilliseconds("Legenda") {
		t.Fatalf("delay = %#v, want proportional typing delay", payload["delay"])
	}
	if _, exists := payload["mediatype"]; exists {
		t.Fatal("payload must not contain unsupported mediatype field")
	}
	if _, exists := payload["media"]; exists {
		t.Fatal("payload must not contain unsupported media field")
	}
}

func TestBuildDocumentPayloadPreservesOriginalName(t *testing.T) {
	payload := buildMediaPayload("5511999999999", "document", "https://example.com/file.pdf", "", "Proposta comercial.pdf")
	if payload["filename"] != "Proposta comercial.pdf" {
		t.Fatalf("filename = %#v", payload["filename"])
	}
}
