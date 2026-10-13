package middlewares

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func TestWebhookAcceptsPhoneImageLargerThanOldBodyLimit(t *testing.T) {
	connection := repo.ConnectionEVO{Id: 1, InstanceID: uuid.New(), ConnectionKey: uuid.New(), Webhook: uuid.New()}
	key := connection.Webhook.String()
	webhookConnectionCache.Lock()
	webhookConnectionCache.items[key] = webhookConnectionCacheEntry{Connection: connection, ExpiresAt: time.Now().Add(time.Minute)}
	webhookConnectionCache.Unlock()
	t.Cleanup(func() {
		webhookConnectionCache.Lock()
		delete(webhookConnectionCache.items, key)
		webhookConnectionCache.Unlock()
	})
	app := iris.New()
	app.Post("/webhook/{webhookId:string}", AuthWebhook, func(ctx iris.Context) {
		_, _, body, ok := WebhookPayload(ctx)
		if !ok || len(body) <= 8<<20 {
			t.Error("large webhook content lost")
		}
		ctx.StatusCode(204)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"event": "Message", "instanceId": connection.InstanceID, "instanceToken": connection.ConnectionKey, "data": map[string]any{"Message": map[string]any{"imageMessage": map[string]string{"mimetype": "image/jpeg"}, "base64": strings.Repeat("A", 9<<20)}}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("POST", "/webhook/"+key, strings.NewReader(string(body))))
	if response.Code != 204 {
		t.Fatalf("phone image rejected: %d %s", response.Code, response.Body.String())
	}
}
