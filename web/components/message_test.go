package components

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"
)

func TestEditedMessagesReplaceOriginalBubble(t *testing.T) {
	message := repo.MessageData{MessageId: "original", Text: "Texto editado", MediaType: "text", DisplayData: json.RawMessage(`{"edited":true}`)}
	for _, sent := range []bool{true, false} {
		var output bytes.Buffer
		var err error
		if sent {
			err = MessageSent(message, 1, "conversation", true).Render(context.Background(), &output)
		} else {
			err = MessageReceived(message, 1, 7, "conversation", true).Render(context.Background(), &output)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`id="message-original"`, `hx-swap-oob="outerHTML"`, "Texto editado", "data-message-edited", "Editada"} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("edited bubble missing %q", want)
			}
		}
	}
}

func TestMessageTimeLabelUsesCompanyTimezone(t *testing.T) {
	messageTime := time.Date(2026, time.September, 11, 16, 0, 0, 0, time.UTC)
	if got := messageTimeLabel(messageTime, "America/Rio_Branco", "15:04"); got != "11:00" {
		t.Fatalf("messageTimeLabel() = %q, want 11:00", got)
	}
}

func TestMessageDateLabelUsesRelativeDays(t *testing.T) {
	now := time.Date(2026, time.September, 17, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		value time.Time
		want  string
	}{
		{value: now.Add(-time.Hour), want: "Hoje"},
		{value: now.AddDate(0, 0, -1), want: "Ontem"},
		{value: time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC), want: "10/09/2026"},
	}
	for _, test := range tests {
		if got := messageDateLabel(test.value, "UTC", now); got != test.want {
			t.Fatalf("messageDateLabel(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestMessagePreservesLineBreaks(t *testing.T) {
	message := repo.MessageData{Text: "Primeira linha\n\nSegunda linha", MediaType: "text"}
	var output bytes.Buffer
	if err := MessageSent(message, 1, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render message: %v", err)
	}
	html := output.String()
	if !strings.Contains(html, "whitespace-pre-wrap") {
		t.Fatalf("message does not preserve whitespace: %s", html)
	}
	if !strings.Contains(html, "Primeira linha\n\nSegunda linha") {
		t.Fatalf("message line breaks were removed: %s", html)
	}
}

func TestPhoneImageCanReplacePlaceholderWithoutDuplicatingMessage(t *testing.T) {
	message := repo.MessageData{MessageId: "phone:image", MediaType: "image", MediaPath: "phone.jpg", IsFromMe: true}
	var output bytes.Buffer
	if err := MessageSent(message, 1, "conversation", true).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="message-phone-image"`, `hx-swap-oob="outerHTML"`, `data-message-id="phone:image"`, `justify-end`, `<img`, `phone.jpg/content`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("media update missing %q", want)
		}
	}
}

func TestMessageRendersClickableLinkPreview(t *testing.T) {
	message := repo.MessageData{Text: "Confira https://example.com/oferta.", MediaType: "text"}
	var output bytes.Buffer
	if err := MessageReceived(message, 1, 9, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`href="https://example.com/oferta"`, `target="_blank"`, "example.com", "fa-link"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("link preview does not contain %q: %s", expected, html)
		}
	}
}

func TestMessageRendersQuoteAndReactions(t *testing.T) {
	message := repo.MessageData{
		MessageId:  "message:1",
		Text:       "Resposta",
		MediaType:  "text",
		QuotedId:   "quoted-id",
		QuotedText: "Mensagem original",
		Reactions:  []repo.MessageReaction{{Emoji: "👍", Count: 2, Mine: true}},
	}
	var output bytes.Buffer
	if err := MessageSent(message, 1, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"Mensagem original", "message-reactions-message-1", "👍", ">2<", "data-reply-message-id"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("message markup does not contain %q: %s", expected, html)
		}
	}
}

func TestGroupMessageRendersSenderIdentity(t *testing.T) {
	message := repo.MessageData{
		MessageId:       "group-message",
		Text:            "Mensagem do grupo",
		MediaType:       "text",
		IsGroup:         true,
		SenderName:      "Rafael Lima",
		SenderNumber:    "5511999999999",
		SenderContactId: 42,
		CreatedAt:       time.Date(2026, time.September, 17, 15, 42, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, time.September, 17, 19, 1, 0, 0, time.UTC),
		Timezone:        "UTC",
	}
	var output bytes.Buffer
	if err := MessageReceived(message, 1, 9, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render group message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"Rafael Lima (+5511999999999)", `/management/api/contacts/42/avatar`, "Mensagem do grupo", `data-message-time`, `datetime="2026-09-17T15:42:00Z"`, ">15:42</time>"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("group message does not contain %q: %s", expected, html)
		}
	}
}

func TestGroupGalleryShowsSenderTime(t *testing.T) {
	messages := []repo.MessageData{{MessageId: "photo-1", MediaType: "image", MediaPath: "one.jpg", IsGroup: true, SenderName: "Rafael", CreatedAt: time.Date(2026, time.September, 17, 15, 42, 0, 0, time.UTC), Timezone: "UTC"}, {MessageId: "photo-2", MediaType: "image", MediaPath: "two.jpg", IsGroup: true, SenderName: "Rafael", CreatedAt: time.Date(2026, time.September, 17, 15, 43, 0, 0, time.UTC), Timezone: "UTC"}}
	var output bytes.Buffer
	if err := MessageReceivedGallery(messages, 1, 9, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Rafael", `data-message-time`, `datetime="2026-09-17T15:43:00Z"`, ">15:43</time>"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("group gallery does not contain %q", expected)
		}
	}
}

func TestMessageRendersScopeAwareDocument(t *testing.T) {
	message := repo.MessageData{MessageId: "document-message", MediaType: "document", MediaScope: "flows", MediaPath: "proposal.pdf", DisplayData: json.RawMessage(`{"mimeType":"application/pdf","fileName":"Proposta.pdf"}`)}
	var output bytes.Buffer
	if err := MessageSent(message, 1, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render document message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`data-message-id="document-message"`, `data-media-mime="application/pdf"`, `/management/api/media/flows/proposal.pdf/download`, "Proposta.pdf", "Documento"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("document message does not contain %q: %s", expected, html)
		}
	}
}

func TestMessageRendersCustomAudioPlayer(t *testing.T) {
	message := repo.MessageData{MessageId: "audio-message", MediaType: "audio", MediaPath: "voice.ogg"}
	var output bytes.Buffer
	if err := MessageReceived(message, 1, 9, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render audio message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"data-audio-player", "data-play-btn", "data-volume-control", "data-speed-control", `preload="metadata"`, "/management/api/media/messages/voice.ogg/content"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("audio message does not contain %q: %s", expected, html)
		}
	}
	if strings.Contains(html, `onload="initAudioPlayer(this)"`) {
		t.Fatal("audio player still uses ineffective inline initialization")
	}
}

func TestMessageTimeRendersInsideBubble(t *testing.T) {
	message := repo.MessageData{MessageId: "timed-message", Text: "Olá", MediaType: "text", UpdatedAt: time.Date(2026, time.September, 17, 12, 34, 56, 0, time.UTC), Timezone: "UTC"}
	var output bytes.Buffer
	if err := MessageSent(message, 1, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render timed message: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"data-message-date=\"2026-09-17\"", `data-message-time`, `datetime="2026-09-17T12:34:56Z"`, ">12:34</time>", "message-status-timed-message"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("timed message does not contain %q: %s", expected, html)
		}
	}
	if strings.Contains(html, ">12:34:56<") {
		t.Fatal("message still renders seconds outside the bubble")
	}
}

func TestReceivedIndividualMessageRendersTimestamp(t *testing.T) {
	message := repo.MessageData{MessageId: "received-timed-message", Text: "Olá", MediaType: "text", CreatedAt: time.Date(2026, time.September, 17, 12, 34, 0, 0, time.UTC), Timezone: "UTC"}
	var output bytes.Buffer
	if err := MessageReceived(message, 1, 9, "conversation-id").Render(context.Background(), &output); err != nil {
		t.Fatalf("render received message: %v", err)
	}
	for _, expected := range []string{`data-message-time`, `datetime="2026-09-17T12:34:00Z"`, "fa-clock", ">12:34</time>"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("received message missing visible timestamp %q", expected)
		}
	}
}
