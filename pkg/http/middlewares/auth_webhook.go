package middlewares

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	repo2 "sophus/internal/repo"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

const (
	webhookConnectionContextKey = "webhookConnection"
	webhookEventContextKey      = "webhookEvent"
	webhookBodyContextKey       = "webhookBody"
)

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
	connection, err := repo2.GetConnectionByWebhook(webhookId)
	if err != nil {
		log.Printf("Evolution GO webhook rejected: connection not found for webhook %s: %v", webhookId, err)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		log.Printf("Evolution GO webhook rejected: could not read body for webhook %s: %v", webhookId, err)
		ctx.StopWithStatus(iris.StatusInternalServerError)
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
