package repo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckFlowMessageTypeDetectsMediaFromRawPayload(t *testing.T) {
	message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"imageMessage":{"url":"https://example.test/image.jpg"}}}}`)}
	if got := CheckFlowMessageType(message); got != "image" {
		t.Fatalf("CheckFlowMessageType() = %q, want image", got)
	}
}

func TestCheckFlowMessageTypeUsesMediaTypeVariants(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Info.Mediatype = "documentMessage"
	if got := CheckFlowMessageType(message); got != "document" {
		t.Fatalf("CheckFlowMessageType() = %q, want document", got)
	}
}

func TestNormalizeFlowInboundMessageSupportsClientPayloadVariants(t *testing.T) {
	tests := []struct {
		name, payload, text, messageType string
	}{
		{
			name:        "mobile image without caption",
			payload:     `{"data":{"Message":{"imageMessage":{"mimetype":"image/jpeg"}}}}`,
			messageType: "image",
		},
		{
			name:        "desktop document with caption",
			payload:     `{"data":{"message":{"documentMessage":{"caption":"Contrato enviado"}}}}`,
			text:        "Contrato enviado",
			messageType: "document",
		},
		{
			name:        "web list response",
			payload:     `{"data":{"message":{"listResponseMessage":{"singleSelectReply":{"selectedRowId":"support"}}}}}`,
			text:        "support",
			messageType: "interactive",
		},
		{
			name:        "web extended text",
			payload:     `{"data":{"message":{"extendedTextMessage":{"text":"olá"}}}}`,
			text:        "olá",
			messageType: "text",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inbound := NormalizeFlowInboundMessage(EventMessageEVO{FullJSON: []byte(test.payload)})
			if inbound.Text != test.text || inbound.Type != test.messageType {
				t.Fatalf("NormalizeFlowInboundMessage() = %#v, want text=%q type=%q", inbound, test.text, test.messageType)
			}
		})
	}
}

func TestCanonicalWhatsAppIdentityClassifiesPNAndLIDBySuffix(t *testing.T) {
	number, jid, lid := canonicalWhatsAppIdentity("154318846066895:23@lid", "5511915079318:23@s.whatsapp.net")
	if number != "5511915079318" || jid != "5511915079318@s.whatsapp.net" || lid != "154318846066895@lid" {
		t.Fatalf("canonical identity = (%q, %q, %q)", number, jid, lid)
	}
}

func TestCanonicalWhatsAppIdentityAcceptsFormattedPhone(t *testing.T) {
	number, jid, lid := canonicalWhatsAppIdentity("+55 (11) 99999-9999")
	if number != "5511999999999" || jid != "5511999999999@s.whatsapp.net" || lid != "" {
		t.Fatalf("canonical identity = (%q, %q, %q)", number, jid, lid)
	}
}

func TestMessageIsFromConnectionDetectsPhoneOutboundMessage(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Info.Sender = "5511999999999@s.whatsapp.net"
	if !messageIsFromConnection(message, ConnectionEVO{Number: "55 (11) 99999-9999"}) {
		t.Fatal("message sent by the connected phone was not identified as outgoing")
	}
	message.Data.Info.Sender = "5511888888888@s.whatsapp.net"
	if messageIsFromConnection(message, ConnectionEVO{Number: "5511999999999"}) {
		t.Fatal("incoming message was identified as outgoing")
	}
}

func TestNormalizeMessageDirectionKeepsIncomingMessageInbound(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Info.Sender = "5511888888888@s.whatsapp.net"
	NormalizeMessageDirection(&message, ConnectionEVO{Number: "5511999999999"})
	if message.Data.Info.IsFromMe {
		t.Fatal("incoming message direction was changed")
	}
}

func TestNormalizeMessageDirectionUsesWebhookMessageKey(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Message.Key.FromMe = true
	NormalizeMessageDirection(&message, ConnectionEVO{})
	if !message.Data.Info.IsFromMe {
		t.Fatal("message key fromMe flag was not preserved")
	}
}

func TestCanonicalWhatsAppIdentityDoesNotUseLIDAsPhone(t *testing.T) {
	number, jid, lid := canonicalWhatsAppIdentity("154318846066895@lid")
	if number != "" || jid != "" || lid != "154318846066895@lid" {
		t.Fatalf("canonical identity = (%q, %q, %q)", number, jid, lid)
	}
}

func TestWhatsAppParticipantPrefersPhoneJIDAndFallsBackToLID(t *testing.T) {
	if got := WhatsAppParticipant("154318846066895@lid", "5511915079318@s.whatsapp.net"); got != "5511915079318@s.whatsapp.net" {
		t.Fatalf("WhatsAppParticipant() = %q, want phone JID", got)
	}
	if got := WhatsAppParticipant("154318846066895@lid"); got != "154318846066895@lid" {
		t.Fatalf("WhatsAppParticipant() = %q, want LID", got)
	}
}

func TestQuotedMessageIDSupportsMediaContext(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Message.IMG.ContextInfo.QuotedMessageID = "quoted-image"
	if got := QuotedMessageID(message); got != "quoted-image" {
		t.Fatalf("QuotedMessageID() = %q, want quoted-image", got)
	}
}

func TestEvolutionQuoteUsesParticipantField(t *testing.T) {
	message := TextMessageEVO{MessageBaseEVO: MessageBaseEVO{
		Number: "120363000000000000@g.us",
		QuotedEVO: QuotedEVO{
			MessageID:   "target-id",
			Participant: "5511999999999@s.whatsapp.net",
		},
	}, Text: "Resposta"}
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal quote payload: %v", err)
	}
	jsonPayload := string(payload)
	if !strings.Contains(jsonPayload, `"quoted":{"messageId":"target-id","participant":"5511999999999@s.whatsapp.net"}`) {
		t.Fatalf("unexpected quote payload: %s", jsonPayload)
	}
	if strings.Contains(jsonPayload, "messageParticipant") {
		t.Fatalf("legacy participant field remains in payload: %s", jsonPayload)
	}
}

func TestTextMessageSendIncludesCurrentAndLegacyQuoteParticipant(t *testing.T) {
	var received struct {
		Quoted struct {
			MessageID          string `json:"messageId"`
			Participant        string `json:"participant"`
			MessageParticipant string `json:"messageParticipant"`
		} `json:"quoted"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode Evolution request: %v", err)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()
	originalBaseURL := ApiBaseURL
	ApiBaseURL = server.URL
	defer func() { ApiBaseURL = originalBaseURL }()

	message := TextMessageEVO{MessageBaseEVO: MessageBaseEVO{
		Number: "5511999999999",
		QuotedEVO: QuotedEVO{
			MessageID:   "target-id",
			Participant: "5511888888888@s.whatsapp.net",
		},
	}, Text: "Resposta"}
	status, _, err := message.Send("api-key")
	if err != nil || status != http.StatusOK {
		t.Fatalf("Send() status=%d err=%v", status, err)
	}
	if received.Quoted.MessageID != "target-id" || received.Quoted.Participant != "5511888888888@s.whatsapp.net" || received.Quoted.MessageParticipant != received.Quoted.Participant {
		t.Fatalf("unexpected quote payload: %#v", received.Quoted)
	}
}

func TestEvolutionReactionWebhookShape(t *testing.T) {
	payload := []byte(`{"data":{"Info":{"ID":"reaction-id","Sender":"5511888888888@s.whatsapp.net","IsFromMe":false},"Message":{"reactionMessage":{"key":{"remoteJID":"120363000000000000@g.us","fromMe":false,"ID":"target-id","participant":"5511999999999@s.whatsapp.net"},"text":"👍"}}}}`)
	var message EventMessageEVO
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("unmarshal reaction webhook: %v", err)
	}
	if message.Data.Message.Reaction.Key.ID != "target-id" || message.Data.Message.Reaction.Text != "👍" {
		t.Fatalf("unexpected reaction: %#v", message.Data.Message.Reaction)
	}
}
