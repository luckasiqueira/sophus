package middlewares

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	repo2 "sophus/internal/repo"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

const (
	webhookConnectionContextKey = "webhookConnection"
	webhookEventContextKey      = "webhookEvent"
	webhookBodyContextKey       = "webhookBody"
	// 50 MiB attachments expand to about 67 MiB in base64, plus JSON metadata.
	maxWebhookBodyBytes = 72 << 20
)

type webhookConnectionCacheEntry struct {
	Connection repo2.ConnectionEVO
	ExpiresAt  time.Time
}

var webhookConnectionCache = struct {
	sync.Mutex
	items map[string]webhookConnectionCacheEntry
}{items: make(map[string]webhookConnectionCacheEntry)}

func AuthWebhook(ctx iris.Context) {
	connection, event, body, err := validateWebhook(ctx)
	if err != nil {
		return
	}
	ctx.Values().Set(webhookConnectionContextKey, connection)
	ctx.Values().Set(webhookEventContextKey, event)
	ctx.Values().Set(webhookBodyContextKey, body)
	ctx.Next()
}

func WebhookPayload(ctx iris.Context) (repo2.ConnectionEVO, repo2.EventEVO, []byte, bool) {
	connection, connectionOK := ctx.Values().Get(webhookConnectionContextKey).(repo2.ConnectionEVO)
	event, eventOK := ctx.Values().Get(webhookEventContextKey).(repo2.EventEVO)
	body, bodyOK := ctx.Values().Get(webhookBodyContextKey).([]byte)
	return connection, event, body, connectionOK && eventOK && bodyOK
}

func validateWebhook(ctx iris.Context) (repo2.ConnectionEVO, repo2.EventEVO, []byte, error) {
	webhookId := ctx.Params().Get("webhookId")
	_, err := uuid.Parse(webhookId)
	if webhookId == "" || err != nil {
		log.Printf("Evolution GO webhook rejected: invalid webhook ID")
		ctx.StopWithStatus(iris.StatusBadRequest)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	connection, err := cachedWebhookConnection(webhookId)
	if err != nil {
		log.Printf("Evolution GO webhook rejected: connection not found")
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxWebhookBodyBytes)
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		log.Printf("Evolution GO webhook rejected: could not read body for connection %d: %v", connection.Id, err)
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			ctx.StopWithStatus(iris.StatusRequestEntityTooLarge)
		} else {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	var event repo2.EventEVO
	err = json.Unmarshal(body, &event)
	if err != nil {
		log.Printf("Evolution GO webhook rejected: invalid JSON for connection %d: %v", connection.Id, err)
		ctx.StopWithStatus(iris.StatusBadRequest)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	if event.InstanceID != connection.InstanceID {
		log.Printf("Evolution GO webhook rejected: instance mismatch for connection %d", connection.Id)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, errors.New("webhook instance does not match connection")
	}
	if event.InstanceToken != connection.ConnectionKey {
		log.Printf("Evolution GO webhook rejected: token mismatch for connection %d", connection.Id)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, errors.New("webhook token does not match connection")
	}
	return connection, event, body, nil
}

func cachedWebhookConnection(webhookID string) (repo2.ConnectionEVO, error) {
	now := time.Now()
	webhookConnectionCache.Lock()
	defer webhookConnectionCache.Unlock()
	if cached, ok := webhookConnectionCache.items[webhookID]; ok && now.Before(cached.ExpiresAt) {
		return cached.Connection, nil
	}
	connection, err := repo2.GetConnectionByWebhook(webhookID)
	if err != nil {
		delete(webhookConnectionCache.items, webhookID)
		return repo2.ConnectionEVO{}, err
	}
	webhookConnectionCache.items[webhookID] = webhookConnectionCacheEntry{Connection: connection, ExpiresAt: now.Add(30 * time.Second)}
	if len(webhookConnectionCache.items) > 10000 {
		for key, entry := range webhookConnectionCache.items {
			if !now.Before(entry.ExpiresAt) {
				delete(webhookConnectionCache.items, key)
			}
		}
	}
	return connection, nil
}
