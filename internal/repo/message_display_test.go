package repo

import (
	"strings"
	"testing"
)

func TestReceivedMessageFormats(t *testing.T) {
	tests := []struct{ name, message, kind, text string }{
		{"list", `{"listMessage":{"title":"Atendimento","description":"Escolha uma opção","sections":[{"title":"Serviços","rows":[{"rowId":"support","title":"Suporte","description":"Ajuda técnica"}]}],"contextInfo":{"stanzaID":"original"}}}`, "list", "Ajuda técnica"},
		{"native list", `{"interactiveMessage":{"body":{"text":"Escolha"},"nativeFlowMessage":{"buttons":[{"name":"single_select","buttonParamsJson":"{\"title\":\"Ver serviços\",\"sections\":[{\"rows\":[{\"id\":\"sales\",\"title\":\"Vendas\"}]}]}"}]}}}`, "list", "Vendas"},
		{"buttons", `{"buttonsMessage":{"contentText":"Podemos ajudar?","buttons":[{"buttonId":"yes","buttonText":{"displayText":"Sim"}}]}}`, "buttons", "Sim"},
		{"template", `{"templateMessage":{"hydratedTemplate":{"hydratedContentText":"Bem-vindo","hydratedButtons":[{"quickReplyButton":{"displayText":"Começar","id":"start"}}]}}}`, "buttons", "Começar"},
		{"poll", `{"pollCreationMessageV3":{"name":"Qual horário?","options":[{"optionName":"Manhã"}]}}`, "poll", "Manhã"},
		{"contact", `{"contactMessage":{"displayName":"Maria","vcard":"BEGIN:VCARD\nFN:Maria\nTEL;waid=5511999999999:+5511999999999\nEND:VCARD"}}`, "contact", "+5511999999999"},
		{"location", `{"locationMessage":{"degreesLatitude":0,"degreesLongitude":-46.63}}`, "location", "0.000000, -46.630000"},
		{"animated sticker", `{"stickerMessage":{"mimetype":"image/webp","isAnimated":true}}`, "sticker", ""},
		{"wrapped document", `{"documentWithCaptionMessage":{"message":{"documentMessage":{"fileName":"Contrato.pdf","mimetype":"application/pdf","caption":"Contrato enviado"}}}}`, "document", "Contrato enviado"},
		{"ephemeral text", `{"ephemeralMessage":{"message":{"extendedTextMessage":{"text":"Olá"}}}}`, "text", "Olá"},
		{"wrapped response", `{"ephemeralMessage":{"message":{"listResponseMessage":{"singleSelectReply":{"selectedRowId":"support"}}}}}`, "interactive", "support"},
		{"unknown", `{"futureMessage":{"secret":"unsupported"}}`, "unknown", "futureMessage"},
		{"null variants", `{"listMessage":null,"stickerMessage":null,"conversation":"Texto normal"}`, "text", "Texto normal"},
		{"view once", `{"viewOnceMessageV2":{"message":{"imageMessage":{"caption":"private caption","mimetype":"image/jpeg"}}}}`, "view_once", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":` + test.message + `}}`)}
			if got := CheckMessageType(message); got != test.kind {
				t.Fatalf("kind=%q, want %q", got, test.kind)
			}
			text := CheckMessageText(message)
			if test.text != "" && !strings.Contains(text, test.text) {
				t.Fatalf("text=%q, missing %q", text, test.text)
			}
			if test.kind == "view_once" && (text != "" || decodeInboundDisplay(message).Display.MimeType != "") {
				t.Fatalf("view-once content exposed: %q", text)
			}
			if test.name == "list" && QuotedMessageID(message) != "original" {
				t.Fatal("menu quotation lost")
			}
			if test.name == "animated sticker" && !decodeInboundDisplay(message).Display.Animated {
				t.Fatal("animation metadata lost")
			}
		})
	}
}

func TestReceivedMediaExtension(t *testing.T) {
	for _, test := range []struct{ mime, name, want string }{
		{"image/webp", "", "webp"}, {"audio/webm", "recording.webm", "weba"}, {"application/octet-stream", "Contrato.DOCX", "docx"}, {"application/unknown", "../../bad.<script>", "bin"}, {"application/unknown", "file.averylongextension", "bin"}, {"application/unknown", "", "bin"},
	} {
		if got := receivedMediaExtension(test.mime, test.name); got != test.want {
			t.Errorf("extension(%q,%q)=%q, want %q", test.mime, test.name, got, test.want)
		}
	}
}
