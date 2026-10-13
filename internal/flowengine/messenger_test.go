package flowengine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestChatMediaSendsWithoutDelayAndWithoutReloadingContact(t *testing.T) {
	oldURL := repo.ApiBaseURL
	t.Cleanup(func() { repo.ApiBaseURL = oldURL })
	var received int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send/media" {
			t.Errorf("unexpected external lookup: %s", r.URL.Path)
		}
		var payload struct {
			Delay  int    `json:"delay"`
			Number string `json:"number"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Delay != 0 || payload.Number != "5511999999999" {
			t.Errorf("chat payload: %+v", payload)
		}
		received++
		// Stop before persistence: this test exercises the real HTTP payload,
		// not a database fixture or an actual WhatsApp message.
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	repo.ApiBaseURL = server.URL
	settings := repo.CompanyOperationSettings{AllowedAttachmentTypes: []string{"image", "audio", "document", "video"}}
	connection := repo.ConnectionEVO{Id: 11, CompanyID: 3}
	conversation := repo.Conversation{Id: 8, ConnectionID: 11, Contact: repo.Contact{Id: 5, CompanyId: 3, Number: "5511999999999"}}
	for _, kind := range settings.AllowedAttachmentTypes {
		if _, err := NewChatMessenger(connection, settings).SendStoredMediaMessage(conversation, kind, "https://example.test/file", "Legenda longa para envio manual", "file", "messages", "file"); err == nil {
			t.Fatal("expected provider error")
		}
	}
	if received != 4 {
		t.Fatalf("manual sends: %d", received)
	}
	if got := NewMessenger(connection).typingDelay("Texto do fluxo"); got != repo.TypingDelayMilliseconds("Texto do fluxo") || got == 0 {
		t.Fatalf("flow delay changed: %d", got)
	}
}

func TestBuildDocumentPayloadPreservesOriginalName(t *testing.T) {
	payload := buildMediaPayload("5511999999999", "document", "https://example.com/file.pdf", "", "Proposta comercial.pdf")
	if payload["filename"] != "Proposta comercial.pdf" {
		t.Fatalf("filename = %#v", payload["filename"])
	}
}
