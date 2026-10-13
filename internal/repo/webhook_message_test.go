package repo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhoneMessageNormalizationPreservesDestinationAndMedia(t *testing.T) {
	for _, test := range []struct {
		name, payload, kind, chat, text string
		group                           bool
	}{
		{"device metadata", `{"data":{"Info":{"ID":"phone-image","Chat":"5511999999999@s.whatsapp.net","Sender":"123456789@lid","SenderAlt":"5511999999999@s.whatsapp.net","DeviceSentMeta":{"DestinationJID":"5511888888888@s.whatsapp.net"}},"Message":{"imageMessage":{"mimetype":"image/jpeg","caption":"Foto enviada"},"base64":"aW1hZ2U="}}}`, "image", "5511888888888@s.whatsapp.net", "Foto enviada", false},
		{"device wrapper", `{"data":{"Info":{"ID":"phone-video"},"Message":{"deviceSentMessage":{"destinationJID":"5511888888888@s.whatsapp.net","message":{"videoMessage":{"mimetype":"video/mp4","caption":"Vídeo enviado"}}},"base64":"aW1hZ2U="}}}`, "video", "5511888888888@s.whatsapp.net", "Vídeo enviado", false},
		{"group device wrapper", `{"data":{"Info":{"ID":"phone-group"},"Message":{"deviceSentMessage":{"destinationJID":"120363123456789@g.us","message":{"conversation":"Texto no grupo"}}}}}`, "text", "120363123456789@g.us", "Texto no grupo", true},
		{"message key", `{"data":{"key":{"id":"phone-key","fromMe":true,"remoteJid":"5511888888888@s.whatsapp.net"},"Message":{"documentMessage":{"mimetype":"application/pdf","caption":"Documento"}}}}`, "document", "5511888888888@s.whatsapp.net", "Documento", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var message EventMessageEVO
			if err := json.Unmarshal([]byte(test.payload), &message); err != nil {
				t.Fatal(err)
			}
			message.FullJSON = []byte(test.payload)
			NormalizeWebhookMessage(&message, ConnectionEVO{Number: "5511999999999"})
			if !message.Data.Info.IsFromMe || message.Data.Info.Chat != test.chat || message.Data.Info.IsGroup != test.group {
				t.Fatalf("incorrect phone message identity: %+v", message.Data.Info)
			}
			if CheckMessageType(message) != test.kind || CheckMessageText(message) != test.text {
				t.Fatalf("phone content lost: type=%s text=%q", CheckMessageType(message), CheckMessageText(message))
			}
			if strings.Contains(test.payload, "base64") {
				if message.Data.Message.Base64 != "aW1hZ2U=" || strings.Contains(string(message.FullJSON), "aW1hZ2U=") {
					t.Fatal("media bytes lost or retained in metadata projection")
				}
				NormalizeWebhookMessage(&message, ConnectionEVO{Number: "5511999999999"})
				if message.Data.Message.Base64 != "aW1hZ2U=" {
					t.Fatal("save normalization erased downloaded media")
				}
			}
		})
	}
}

func TestDownloadWebhookMediaUsesConnectionAndUnwrappedMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/message/downloadmedia" || r.Method != "POST" || r.Header.Get("apikey") != "instance-key" {
			t.Errorf("unexpected media request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Message map[string]json.RawMessage `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, ok := payload.Message["imageMessage"]; !ok {
			t.Error("media wrapper was not unwrapped")
		}
		if _, ok := payload.Message["deviceSentMessage"]; ok {
			t.Error("unsupported device wrapper sent to downloader")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"base64":"data:image/jpeg;base64,aW1hZ2U="}}`))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"deviceSentMessage":{"destinationJID":"5511888888888@s.whatsapp.net","message":{"imageMessage":{"url":"https://mmg.whatsapp.net/encrypted","mimetype":"image/jpeg","mediaKey":"a2V5"}}}}}}`)}
	NormalizeWebhookMessage(&message, ConnectionEVO{})
	if err := DownloadWebhookMedia(&message, ConnectionEVO{APIToken: "instance-key"}); err != nil {
		t.Fatal(err)
	}
	NormalizeWebhookMessage(&message, ConnectionEVO{})
	if message.Data.Message.Base64 != "data:image/jpeg;base64,aW1hZ2U=" {
		t.Fatal("downloaded media lost")
	}
	if err := DownloadWebhookMedia(&message, ConnectionEVO{APIToken: "instance-key"}); err != nil {
		t.Fatal(err)
	}
	private := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"viewOnceMessageV2":{"message":{"imageMessage":{"url":"https://mmg.whatsapp.net/private"}}}}}}`)}
	if err := DownloadWebhookMedia(&private, ConnectionEVO{APIToken: "instance-key"}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("download repeated or view-once media fetched: %d", requests)
	}
}

func TestDownloadWebhookMediaPropagatesProviderFailureForRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"error":"temporary failure"}`))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"imageMessage":{"url":"https://mmg.whatsapp.net/encrypted"}}}}`)}
	if err := DownloadWebhookMedia(&message, ConnectionEVO{}); err == nil {
		t.Fatal("provider failure swallowed; queue cannot retry")
	}
	if message.Data.Message.Base64 != "" {
		t.Fatal("error response used as media content")
	}
}

func TestTechnicalWhatsAppEventsAreNotChatMessages(t *testing.T) {
	for _, payload := range []string{
		`{"data":{"Message":{"protocolMessage":{"type":5}}}}`,
		`{"data":{"Message":{"ephemeralMessage":{"message":{"protocolMessage":{"type":"HISTORY_SYNC_NOTIFICATION"}}}}}}`,
		`{"data":{"Message":{"senderKeyDistributionMessage":{"groupId":"test"}}}}`,
	} {
		message := EventMessageEVO{FullJSON: []byte(payload)}
		NormalizeWebhookMessage(&message, ConnectionEVO{})
		if !IsInternalWhatsAppEvent(message) {
			t.Fatalf("technical event accepted: %s", payload)
		}
	}
	message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"conversation":"protocolMessage"}}}`)}
	if IsInternalWhatsAppEvent(message) {
		t.Fatal("real user text was classified as internal event")
	}
}

func TestRetainedMediaSourceExcludesBytesAndViewOnce(t *testing.T) {
	message := EventMessageEVO{FullJSON: []byte(`{"data":{"Message":{"imageMessage":{"url":"https://mmg.whatsapp.net/file","mediaKey":"encrypted-key","base64":"private-file-bytes","JPEGThumbnail":"thumbnail"},"base64":"private-file-bytes"}}}`)}
	source := string(webhookMediaSource(message))
	if !strings.Contains(source, "encrypted-key") || strings.Contains(source, "private-file-bytes") || strings.Contains(source, "thumbnail") {
		t.Fatalf("incorrect recovery descriptor: %s", source)
	}
	message.Data.IsViewOnce = true
	if string(webhookMediaSource(message)) != "{}" {
		t.Fatal("view-once recovery metadata retained")
	}
}

func TestMissingMediaMetadataReturnsExplicitUnavailableError(t *testing.T) {
	message := EventMessageEVO{}
	message.Data.Info.Mediatype = "audio"
	if err := DownloadWebhookMedia(&message, ConnectionEVO{}); err != ErrMediaSourceUnavailable {
		t.Fatalf("missing source silently succeeded: %v", err)
	}
}

func TestLegacyMediaLookupDoesNotPretendToRecoverAbsentFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"result":null}}`))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	message := EventMessageEVO{}
	message.Data.Info.ID = "old-message"
	if err := LoadLegacyMediaSource(&message, ConnectionEVO{}); err != ErrMediaSourceUnavailable {
		t.Fatalf("absent legacy file accepted: %v", err)
	}
}
