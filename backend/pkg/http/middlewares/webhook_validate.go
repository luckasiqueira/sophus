package middlewares

import (
	"encoding/json"
	"io"
	"zubly/backend/internal/database"
	"zubly/backend/pkg/wpp"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func ValidateWebhook(ctx iris.Context) (database.Connection, wpp.Event, []byte, error) {
	webhookId := ctx.Params().Get("webhookId")
	_, err := uuid.Parse(webhookId)
	if webhookId == "" || err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return database.Connection{}, wpp.Event{}, nil, err
	}
	connection, err := database.GetConnectionByWebhook(webhookId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return database.Connection{}, wpp.Event{}, nil, err
	}
	if connection.Status != "connected" {
		ctx.StopWithStatus(iris.StatusLocked)
	}
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return database.Connection{}, wpp.Event{}, nil, err
	}
	var event wpp.Event
	err = json.Unmarshal(body, &event)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return database.Connection{}, wpp.Event{}, nil, err
	}
	if event[0].Body.InstanceID != connection.InstanceID {
		//fmt.Println(e[0].Body.InstanceToken, connection.InstanceID)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return database.Connection{}, wpp.Event{}, nil, err
	}
	return connection, event, body, nil
}
