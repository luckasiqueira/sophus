package waha

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/internal/waprovider"
	"sophus/pkg/http/requests"

	"github.com/google/uuid"
)

const testToken = "0123456789abcdef0123456789abcdef"

var (
	testInstanceID = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	testWebhookID  = uuid.MustParse("66666666-7777-8888-9999-000000000000")
	testConnection = repo.ConnectionEVO{
		Id: 7, Name: "Teste", InstanceID: testInstanceID, Webhook: testWebhookID,
		APIToken: testToken, ConnectionKey: uuid.MustParse(testToken),
	}
	testSession = SessionName(testInstanceID)
)

type recorded struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

type fakeWAHA struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	handle   func(w http.ResponseWriter, r *http.Request, body map[string]any)
}

func (f *fakeWAHA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(apiKeyHeader) != "waha-key" {
		f.t.Errorf("missing WAHA API key on %s %s", r.Method, r.URL.Path)
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	f.handle(w, r, body)
}

func (f *fakeWAHA) find(method, path string) (recorded, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, request := range f.requests {
		if request.Method == method && request.Path == path {
			return request, true
		}
	}
	return recorded{}, false
}

// setup routes Sophus' real Evolution helpers through a bridge backed by a
// fake WAHA server.
func setup(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*Bridge, *fakeWAHA) {
	t.Helper()
	fake := &fakeWAHA{t: t, handle: handle}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	b := &Bridge{
		Client: &Client{BaseURL: server.URL, APIKey: "waha-key", HTTP: server.Client()},
		Resolve: func(key string) (repo.ConnectionEVO, error) {
			if key == testToken {
				return testConnection, nil
			}
			return repo.ConnectionEVO{}, sql.ErrNoRows
		},
		PublicURL: func() (string, error) { return "https://app.example", nil },
		Sleep:     func(context.Context, time.Duration) {},
		Now:       func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	previousBase := repo.ApiBaseURL
	repo.ApiBaseURL = waprovider.BridgeBaseURL
	requests.SetInterceptor(b.Intercept)
	t.Cleanup(func() {
		requests.SetInterceptor(nil)
		repo.ApiBaseURL = previousBase
	})
	return b, fake
}

func write(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sessionPath() string { return "/api/sessions/" + testSession }

func TestIDs(t *testing.T) {
	cases := map[string]string{
		"5511999990000":                   "5511999990000@c.us",
		"+55 (11) 99999-0000":             "5511999990000@c.us",
		"5511999990000:12@s.whatsapp.net": "5511999990000@c.us",
		"120363000000000000@g.us":         "120363000000000000@g.us",
		"123456789@lid":                   "123456789@lid",
	}
	for input, want := range cases {
		if got := ChatID(input); got != want {
			t.Errorf("ChatID(%q) = %q, want %q", input, got, want)
		}
	}
	if got := whatsmeowJID("5511999990000@c.us"); got != "5511999990000@s.whatsapp.net" {
		t.Errorf("whatsmeowJID = %q", got)
	}
	if got := serializedMessageID(false, "120363000000000000@g.us", "ABC", "5511999990000@s.whatsapp.net"); got != "false_120363000000000000@g.us_ABC_5511999990000@c.us" {
		t.Errorf("serializedMessageID = %q", got)
	}
	if got := rawMessageID("true_5511999990000@c.us_3EB0ABC"); got != "3EB0ABC" {
		t.Errorf("rawMessageID = %q", got)
	}
	if id, ok := instanceIDFromSession(testSession); !ok || id != testInstanceID {
		t.Errorf("instanceIDFromSession = %v %v", id, ok)
	}
}

func TestStatusThroughSophusGetStatus(t *testing.T) {
	sessionStatus := "WORKING"
	setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.URL.Path != sessionPath() {
			write(w, 404, map[string]string{"message": "not found"})
			return
		}
		write(w, 200, map[string]any{"name": testSession, "status": sessionStatus,
			"me": map[string]string{"id": "5511999990000@c.us", "pushName": "Loja"}})
	})
	instance := &instances.InstanceEVO{InstanceID: testInstanceID, APIToken: testToken}
	status, err := instance.GetStatus()
	if err != nil || !status.Connected || status.Number != "5511999990000" {
		t.Fatalf("GetStatus = %+v, %v", status, err)
	}
	sessionStatus = "STARTING"
	if status, err := instance.GetStatus(); err != nil || status.Connected {
		t.Fatalf("starting session must not be connected: %+v, %v", status, err)
	}
	unknown := &instances.InstanceEVO{APIToken: "unknown"}
	if _, err := unknown.GetStatus(); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("unknown token must fail like Evolution GO (401), got %v", err)
	}
}

func TestSendTextResponseMatchesEvolution(t *testing.T) {
	_, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch r.URL.Path {
		case "/api/sendText":
			write(w, 201, map[string]any{"id": "true_5511988887777@c.us_3EB0ABC", "_data": map[string]string{"conversation": "Olá"}})
		default:
			write(w, 201, map[string]any{})
		}
	})
	message := repo.TextMessageEVO{Text: "Olá"}
	message.Number = "5511988887777"
	message.Delay = 1200
	message.MessageID = "QUOTED1"
	status, body, err := message.Send(testToken)
	if err != nil || status != 200 {
		t.Fatalf("Send = %d, %v", status, err)
	}
	var event repo.EventMessageEVO
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if event.Data.Info.ID != "3EB0ABC" || event.Data.Info.Chat != "5511988887777@s.whatsapp.net" || !event.Data.Info.IsFromMe {
		t.Fatalf("unexpected Evolution send response: %+v", event.Data.Info)
	}
	sent, ok := fake.find(http.MethodPost, "/api/sendText")
	if !ok || sent.Body["chatId"] != "5511988887777@c.us" || sent.Body["reply_to"] != "QUOTED1" || sent.Body["session"] != testSession || sent.Body["linkPreview"] != false {
		t.Fatalf("unexpected WAHA sendText request: %+v", sent.Body)
	}
	if _, ok := fake.find(http.MethodPost, "/api/startTyping"); !ok {
		t.Fatal("delay must show typing before sending")
	}
}

func TestQRCodeThroughSophusGetQRCode(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.URL.Path == sessionPath():
			write(w, 200, map[string]any{"name": testSession, "status": "SCAN_QR_CODE"})
		case r.URL.Path == "/api/"+testSession+"/auth/qr" && r.URL.Query().Get("format") == "raw":
			write(w, 200, map[string]string{"value": "2@pairing-code"})
		case r.URL.Path == "/api/"+testSession+"/auth/qr":
			write(w, 200, map[string]string{"mimetype": "image/png", "data": png})
		default:
			write(w, 404, map[string]string{"message": "not found"})
		}
	})
	qr, err := (&instances.InstanceEVO{APIToken: testToken}).GetQRCode()
	if err != nil {
		t.Fatal(err)
	}
	if qr.QRCode != "data:image/png;base64,"+png || qr.Code != "2@pairing-code" {
		t.Fatalf("unexpected QR code: %+v", qr)
	}
}

func TestAvatarWithoutPhotoIsNotFound(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 200, map[string]any{"profilePictureURL": nil})
	})
	if _, err := repo.GetWhatsAppAvatar("5511988887777", testToken); !errors.Is(err, repo.ErrAvatarNotFound) {
		t.Fatalf("expected ErrAvatarNotFound, got %v", err)
	}
}

func TestReconnectRecreatesMissingSession(t *testing.T) {
	created := false
	_, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == sessionPath():
			if !created {
				write(w, 404, map[string]string{"message": "Session not found"})
				return
			}
			write(w, 200, map[string]any{"name": testSession, "status": "STOPPED"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/sessions":
			created = true
			write(w, 201, map[string]any{"name": testSession})
		case r.URL.Path == sessionPath()+"/start":
			write(w, 201, map[string]any{"name": testSession})
		default:
			write(w, 404, map[string]string{"message": "not found"})
		}
	})
	if err := (&instances.InstanceEVO{InstanceID: testInstanceID, APIToken: testToken}).Reconnect(); err != nil {
		t.Fatal(err)
	}
	create, ok := fake.find(http.MethodPost, "/api/sessions")
	if !ok || create.Body["name"] != testSession || create.Body["start"] != false {
		t.Fatalf("session was not created: %+v", create.Body)
	}
	config := create.Body["config"].(map[string]any)
	webhook := config["webhooks"].([]any)[0].(map[string]any)
	if webhook["url"] != "https://app.example/webhook/"+testWebhookID.String() ||
		webhook["hmac"].(map[string]any)["key"] != testToken ||
		len(webhook["events"].([]any)) != len(webhookEvents) {
		t.Fatalf("unexpected webhook config: %+v", webhook)
	}
	if _, ok := fake.find(http.MethodPost, sessionPath()+"/start"); !ok {
		t.Fatal("recreated session was not started")
	}
}

func TestButtonsAreSentAsList(t *testing.T) {
	b, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 201, map[string]any{"id": "true_5511988887777@c.us_LIST1"})
	})
	payload, _ := json.Marshal(map[string]any{
		"number": "5511988887777", "title": "Atendimento", "description": "Escolha", "footer": "Sophus",
		"buttons": []map[string]string{{"type": "reply", "displayText": "Suporte", "id": "suporte"}},
	})
	res := b.route(context.Background(), http.MethodPost, "/send/button", testToken, payload)
	if res.status != 200 || !strings.Contains(string(res.body), `"ID":"LIST1"`) {
		t.Fatalf("unexpected response: %d %s", res.status, res.body)
	}
	sent, _ := fake.find(http.MethodPost, "/api/sendList")
	rows := sent.Body["message"].(map[string]any)["sections"].([]any)[0].(map[string]any)["rows"].([]any)
	if row := rows[0].(map[string]any); row["rowId"] != "suporte" || row["title"] != "Suporte" {
		t.Fatalf("button ID must become the list row ID: %+v", row)
	}
}

func TestMenuIsSentAsList(t *testing.T) {
	b, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 201, map[string]any{"id": "true_5511988887777@c.us_MENU1"})
	})
	payload, _ := json.Marshal(map[string]any{
		"number": "5511988887777", "title": "Menu", "description": "Escolha", "buttonText": "Ver Menu", "footerText": "Sophus",
		"sections": []map[string]any{
			{"title": "Opções", "rows": []map[string]string{{"title": "Opção 1", "description": "Teste 1", "rowId": "op1"}, {"title": "Opção 2", "rowId": "op2"}}},
			{"title": "Navegação", "rows": []map[string]string{{"title": "Menu principal", "rowId": "menu_principal"}}},
		},
	})
	res := b.route(context.Background(), http.MethodPost, "/send/list", testToken, payload)
	if res.status != 200 || !strings.Contains(string(res.body), `"ID":"MENU1"`) {
		t.Fatalf("unexpected response: %d %s", res.status, res.body)
	}
	sent, _ := fake.find(http.MethodPost, "/api/sendList")
	message := sent.Body["message"].(map[string]any)
	sections := message["sections"].([]any)
	first := sections[0].(map[string]any)["rows"].([]any)
	if message["button"] != "Ver Menu" || message["footer"] != "Sophus" || len(sections) != 2 {
		t.Fatalf("list message: %+v", message)
	}
	if row := first[0].(map[string]any); row["rowId"] != "op1" || row["description"] != "Teste 1" {
		t.Fatalf("row must keep its ID and description: %+v", row)
	}
	if _, ok := first[1].(map[string]any)["description"]; ok {
		t.Fatal("an empty description must be omitted")
	}
	bad, _ := json.Marshal(map[string]any{"number": "5511988887777", "description": "x", "sections": []map[string]any{{"title": "S", "rows": []map[string]string{{"title": "", "rowId": "a"}}}}})
	if res := b.route(context.Background(), http.MethodPost, "/send/list", testToken, bad); res.status != http.StatusBadRequest {
		t.Fatalf("row without title must be rejected, got %d", res.status)
	}
}

func sign(body []byte) http.Header {
	signer := hmac.New(sha512.New, []byte(testToken))
	signer.Write(body)
	headers := http.Header{}
	headers.Set("X-Webhook-Hmac", hex.EncodeToString(signer.Sum(nil)))
	headers.Set("X-Webhook-Hmac-Algorithm", "sha512")
	return headers
}

func engineEnvelope(t *testing.T, session, event string, data any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "evt_1", "event": "engine.event", "session": session, "engine": "GOWS",
		"payload": map[string]any{"event": event, "data": data},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTranslateWebhookMessage(t *testing.T) {
	b, _ := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 200, map[string]any{"JID": "120363000000000000@g.us", "Name": "Equipe"})
	})
	body := engineEnvelope(t, testSession, "events.Message", map[string]any{
		"Info": map[string]any{
			"Chat": "123456789@lid", "Sender": "123456789:5@lid", "SenderAlt": "5511999990000:3@s.whatsapp.net",
			"ID": "MSG1", "IsFromMe": false, "PushName": "Cliente", "Timestamp": "2026-01-02T03:04:05Z",
		},
		"Message": map[string]any{"imageMessage": map[string]any{
			"URL": "https://mmg.whatsapp.net/x", "mimetype": "image/jpeg", "JPEGThumbnail": "AAAA", "caption": "foto",
		}},
		"RawMessage": map[string]any{"x": 1},
	})
	translated, ignored, err := b.TranslateWebhook(testConnection, sign(body), body)
	if err != nil || ignored {
		t.Fatalf("TranslateWebhook = ignored %v, %v", ignored, err)
	}
	var event repo.EventMessageEVO
	if err := json.Unmarshal(translated, &event); err != nil {
		t.Fatal(err)
	}
	if event.EventType != "Message" || event.InstanceID != testInstanceID || event.InstanceToken != testConnection.ConnectionKey {
		t.Fatalf("unexpected envelope: %+v", event.EventEVO)
	}
	info := event.Data.Info
	if info.Sender != "5511999990000@s.whatsapp.net" || info.Chat != "5511999990000@s.whatsapp.net" || info.SenderAlt != "123456789@lid" {
		t.Fatalf("LID swap not applied like Evolution GO: %+v", info)
	}
	if event.Data.Message.IMG.MimeType != "image/jpeg" || event.Data.Message.IMG.Caption != "foto" {
		t.Fatalf("image content lost: %+v", event.Data.Message.IMG)
	}
	if strings.Contains(string(translated), "JPEGThumbnail") || strings.Contains(string(translated), "RawMessage") {
		t.Fatal("thumbnail and RawMessage must be stripped like Evolution GO")
	}

	group := engineEnvelope(t, testSession, "events.Message", map[string]any{
		"Info":    map[string]any{"Chat": "120363000000000000@g.us", "Sender": "5511999990000@s.whatsapp.net", "ID": "MSG2", "IsGroup": true},
		"Message": map[string]any{"conversation": "oi"},
	})
	translated, _, err = b.TranslateWebhook(testConnection, sign(group), group)
	if err != nil || !strings.Contains(string(translated), `"groupData":{"JID":"120363000000000000@g.us","Name":"Equipe"}`) {
		t.Fatalf("group data not attached: %s %v", translated, err)
	}
}

func TestTranslateWebhookFilters(t *testing.T) {
	b, _ := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) { write(w, 404, map[string]any{}) })
	message := func(content map[string]any) []byte {
		return engineEnvelope(t, testSession, "events.Message", map[string]any{
			"Info": map[string]any{"Chat": "5511999990000@s.whatsapp.net", "ID": "P1"}, "Message": content,
		})
	}
	cases := []struct {
		name    string
		body    []byte
		ignored bool
	}{
		{"key share", message(map[string]any{"protocolMessage": map[string]any{"type": 6}}), true},
		{"revoke without key", message(map[string]any{"protocolMessage": map[string]any{"type": 0}}), true},
		{"revoke", message(map[string]any{"protocolMessage": map[string]any{"type": 0, "key": map[string]any{"ID": "X"}}}), false},
		{"edit", message(map[string]any{"protocolMessage": map[string]any{"type": 14, "key": map[string]any{"ID": "X"}}}), false},
		{"event response", message(map[string]any{"encEventResponseMessage": map[string]any{}}), true},
		{"presence", engineEnvelope(t, testSession, "events.Presence", map[string]any{}), true},
	}
	for _, tc := range cases {
		_, ignored, err := b.TranslateWebhook(testConnection, sign(tc.body), tc.body)
		if err != nil || ignored != tc.ignored {
			t.Errorf("%s: ignored=%v err=%v, want ignored=%v", tc.name, ignored, err, tc.ignored)
		}
	}
}

func TestTranslateWebhookRejectsForgeries(t *testing.T) {
	b, _ := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {})
	body := engineEnvelope(t, testSession, "events.Disconnected", map[string]any{})
	if _, _, err := b.TranslateWebhook(testConnection, http.Header{}, body); !errors.Is(err, ErrWebhookUnauthorized) {
		t.Fatalf("unsigned webhook must be rejected, got %v", err)
	}
	other := engineEnvelope(t, "sophus_other", "events.Disconnected", map[string]any{})
	if _, _, err := b.TranslateWebhook(testConnection, sign(other), other); !errors.Is(err, ErrWebhookUnauthorized) {
		t.Fatalf("webhook for another session must be rejected, got %v", err)
	}
}

func TestTranslateWebhookReceiptAndConnection(t *testing.T) {
	b, _ := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {})
	receipt := engineEnvelope(t, testSession, "events.Receipt", map[string]any{
		"Chat": "5511999990000@s.whatsapp.net", "MessageIDs": []string{"A1", "A2"}, "Type": "read",
	})
	translated, ignored, err := b.TranslateWebhook(testConnection, sign(receipt), receipt)
	if err != nil || ignored {
		t.Fatal(ignored, err)
	}
	ids, status, err := repo.ParseMessageReceipt(translated)
	if err != nil || len(ids) != 2 || status != 3 || !strings.Contains(string(translated), `"state":"Read"`) {
		t.Fatalf("receipt not understood by Sophus: %v %d %v %s", ids, status, err, translated)
	}

	working, _ := json.Marshal(map[string]any{"event": "session.status", "session": testSession, "payload": map[string]string{"status": "WORKING"}})
	translated, ignored, err = b.TranslateWebhook(testConnection, sign(working), working)
	if err != nil || ignored || !strings.Contains(string(translated), `"event":"Connected"`) {
		t.Fatalf("WORKING must become Connected: %s %v %v", translated, ignored, err)
	}
	scanning, _ := json.Marshal(map[string]any{"event": "session.status", "session": testSession, "payload": map[string]string{"status": "SCAN_QR_CODE"}})
	if _, ignored, err := b.TranslateWebhook(testConnection, sign(scanning), scanning); err != nil || !ignored {
		t.Fatalf("SCAN_QR_CODE must be ignored (Sophus polls the QR): %v %v", ignored, err)
	}
	loggedOut := engineEnvelope(t, testSession, "events.LoggedOut", map[string]any{"Reason": 401})
	translated, _, _ = b.TranslateWebhook(testConnection, sign(loggedOut), loggedOut)
	if !strings.Contains(string(translated), `"event":"LoggedOut"`) {
		t.Fatalf("LoggedOut not forwarded: %s", translated)
	}
}

func encryptMedia(t *testing.T, plain, mediaKey []byte, appInfo string) []byte {
	t.Helper()
	iv, cipherKey, macKey, err := expandMediaKey(mediaKey, appInfo)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, _ := aes.NewCipher(cipherKey)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	signer := hmac.New(sha256.New, macKey)
	signer.Write(iv)
	signer.Write(encrypted)
	return append(encrypted, signer.Sum(nil)[:macLength]...)
}

func TestDownloadMediaDecryptsLikeWhatsApp(t *testing.T) {
	plain := []byte("conteudo de teste do arquivo")
	mediaKey := make([]byte, 32)
	if _, err := rand.Read(mediaKey); err != nil {
		t.Fatal(err)
	}
	encrypted := encryptMedia(t, plain, mediaKey, "WhatsApp Document Keys")
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encrypted)
	}))
	defer cdn.Close()
	previous := mediaHTTPClient
	mediaHTTPClient = cdn.Client()
	defer func() { mediaHTTPClient = previous }()

	encSum, plainSum := sha256.Sum256(encrypted), sha256.Sum256(plain)
	media := map[string]any{
		"URL": cdn.URL + "/v/file", "mimetype": "application/pdf",
		"mediaKey":      base64.StdEncoding.EncodeToString(mediaKey),
		"fileEncSHA256": base64.StdEncoding.EncodeToString(encSum[:]),
		"fileSHA256":    base64.StdEncoding.EncodeToString(plainSum[:]),
	}
	b, _ := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {})
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{"documentMessage": media}})
	res := b.route(context.Background(), http.MethodPost, "/message/downloadmedia", testToken, payload)
	var response struct {
		Data struct {
			Base64 string `json:"base64"`
		} `json:"data"`
	}
	if res.status != 200 || json.Unmarshal(res.body, &response) != nil {
		t.Fatalf("download failed: %d %s", res.status, res.body)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(response.Data.Base64); !bytes.Equal(decoded, plain) {
		t.Fatalf("decrypted content mismatch: %q", decoded)
	}

	encrypted[0] ^= 0xff
	if _, err := DownloadMedia(context.Background(), "WhatsApp Document Keys", media); err == nil {
		t.Fatal("tampered media must be rejected")
	}
}

func TestSendResponseUnwrapsGOWSEvent(t *testing.T) {
	_, _ = setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 201, map[string]any{
			"id": "true_5511988887777@c.us_3EB0WRAP",
			"_data": map[string]any{
				"Info":    map[string]any{"ID": "3EB0WRAP", "Chat": "5511988887777@s.whatsapp.net"},
				"Message": map[string]any{"extendedTextMessage": map[string]any{"text": "Olá"}, "messageContextInfo": map[string]any{}},
			},
		})
	})
	message := repo.TextMessageEVO{Text: "Olá"}
	message.Number = "5511988887777"
	_, body, err := message.Send(testToken)
	if err != nil {
		t.Fatal(err)
	}
	event := repo.EventMessageEVO{FullJSON: body}
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if kind := repo.CheckMessageType(event); kind != "text" {
		t.Fatalf("sent text must be stored as text, got %q (%s)", kind, body)
	}
	if event.Data.Message.TXT.Text != "Olá" {
		t.Fatalf("message content not unwrapped: %s", body)
	}
}

func TestSendMediaForwardsQuotedReply(t *testing.T) {
	_, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		write(w, 201, map[string]any{"id": "true_5511988887777@c.us_3EBAUDIO"})
	})
	request := requests.Request{
		URL: repo.ApiBaseURL + "/send/media", Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json", "apikey": testToken},
		Payload: map[string]any{"number": "5511988887777", "type": "audio", "url": "https://app.example/file.ogg",
			"quoted": repo.QuotedEVO{MessageID: "QUOTED1", Participant: "5511988887777@s.whatsapp.net"}},
	}
	if err := request.Do(); err != nil {
		t.Fatal(err)
	}
	sent, ok := fake.find(http.MethodPost, "/api/sendVoice")
	if !ok || sent.Body["reply_to"] != "QUOTED1" {
		t.Fatalf("audio reply must carry reply_to: %+v", sent.Body)
	}
}

func TestReconnectAppliesGOWSStorageConfig(t *testing.T) {
	updated := false
	_, fake := setup(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == sessionPath():
			config := map[string]any{
				"metadata": map[string]string{metadataInstanceID: testInstanceID.String()},
				"webhooks": []map[string]any{{"url": "https://app.example/webhook/" + testWebhookID.String(), "events": webhookEvents}},
			}
			if updated {
				config["gows"] = map[string]any{"storage": map[string]bool{"chats": false, "labels": false}}
			}
			write(w, 200, map[string]any{"name": testSession, "status": "WORKING", "config": config})
		case r.Method == http.MethodPut && r.URL.Path == sessionPath():
			updated = true
			write(w, 200, map[string]any{"name": testSession})
		default:
			write(w, 201, map[string]any{"name": testSession})
		}
	})
	// A distinct instance ID avoids Sophus' 30s per-instance reconnect cooldown
	// left by other tests; the bridge resolves the session from the token.
	if err := (&instances.InstanceEVO{InstanceID: uuid.New(), APIToken: testToken}).Reconnect(); err != nil {
		t.Fatal(err)
	}
	put, ok := fake.find(http.MethodPut, sessionPath())
	if !ok {
		t.Fatal("reconnect must update an outdated session config")
	}
	storage := put.Body["config"].(map[string]any)["gows"].(map[string]any)["storage"].(map[string]any)
	if storage["chats"] != false || storage["labels"] != false {
		t.Fatalf("unexpected GOWS storage config: %+v", storage)
	}
	if _, ok := fake.find(http.MethodPost, sessionPath()+"/restart"); !ok {
		t.Fatal("session must still be restarted")
	}
}
