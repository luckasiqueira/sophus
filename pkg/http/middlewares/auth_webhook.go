package middlewares

import (
	"encoding/json"
	"io"
	repo2 "sophus/internal/repo"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func ValidateWebhook(ctx iris.Context) (repo2.ConnectionEVO, repo2.EventEVO, []byte, error) {
	webhookId := ctx.Params().Get("webhookId")
	_, err := uuid.Parse(webhookId)
	if webhookId == "" || err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	connection, err := repo2.GetConnectionByWebhook(webhookId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	if connection.Status != "connected" {
		ctx.StopWithStatus(iris.StatusLocked)
	}
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	var event repo2.EventEVO
	err = json.Unmarshal(body, &event)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	if event.InstanceID != connection.InstanceID {
		//fmt.Println(e[0].Body.InstanceToken, connection.InstanceID)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo2.ConnectionEVO{}, repo2.EventEVO{}, nil, err
	}
	return connection, event, body, nil
}
