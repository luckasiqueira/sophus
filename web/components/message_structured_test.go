package components

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"sophus/internal/repo"
)

func TestReceivedListRendersEscapedOptions(t *testing.T) {
	display := repo.MessageDisplay{Title: "Atendimento", Body: "Escolha", ButtonText: "Ver opções", Sections: []repo.MessageDisplaySection{{Title: "Serviços", Rows: []repo.MessageDisplayRow{{ID: "support", Title: "Suporte", Description: "<script>alert(1)</script>"}}}}}
	data, err := json.Marshal(display)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := MessageReceived(repo.MessageData{MediaType: "list", DisplayData: data, Text: "summary duplicate"}, 1, 2, "conversation").Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, want := range []string{"<details", "Ver opções", "Serviços", "Suporte", "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, ">summary duplicate<") {
		t.Fatal("unsafe or duplicate menu text rendered")
	}
}

func TestStickerAndMissingContentRendering(t *testing.T) {
	for _, test := range []struct{ kind, path, want string }{
		{"sticker", "sticker.webp", "object-contain"}, {"sticker", "", "Figurinha"}, {"unknown", "", "Conteúdo indisponível"}, {"text", "", "Mensagem sem conteúdo disponível"}, {"document", "", "Documento"},
	} {
		t.Run(test.kind+test.path, func(t *testing.T) {
			var output bytes.Buffer
			if err := messageMedia(repo.MessageData{MediaType: test.kind, MediaPath: test.path}, false).Render(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("missing %q: %s", test.want, output.String())
			}
		})
	}
}

func TestReceivedLocationURLValidatesCoordinates(t *testing.T) {
	zero, lon, bad := 0.0, -46.63, 181.0
	if receivedLocationURL(repo.MessageDisplay{Latitude: &zero, Longitude: &lon}) != "https://www.google.com/maps?q=0.000000,-46.630000" {
		t.Fatal("valid zero latitude rejected")
	}
	if receivedLocationURL(repo.MessageDisplay{Latitude: &zero, Longitude: &bad}) != "" {
		t.Fatal("invalid coordinates accepted")
	}
}

func TestUnavailableMediaOffersRecoveryWhenAuthorizedURLIsProvided(t *testing.T) {
	var output bytes.Buffer
	message := repo.MessageData{Id: 42, MediaType: "audio", MediaRetryURL: "/messages/conversation/media/retry"}
	if err := MessageUnavailable(message, false).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tentar carregar", `data-message-db-id="42"`, `data-media-retry-error`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("recovery control missing %q", want)
		}
	}
}

func TestUnavailableMediaExplainsFullCompanyStorage(t *testing.T) {
	var output bytes.Buffer
	message := repo.MessageData{MediaType: "image", DisplayData: json.RawMessage(`{"mediaUnavailableReason":"storage_quota_exceeded"}`)}
	if err := MessageUnavailable(message, false).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "limite de armazenamento da empresa atingido") || strings.Contains(output.String(), "Conteúdo indisponível") {
		t.Fatal("quota exhaustion was shown as an unknown media failure")
	}
}
