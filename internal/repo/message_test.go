package repo

import "testing"

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
