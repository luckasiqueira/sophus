package middlewares

import (
	"encoding/json"
	"io"
	"sophus/backend/internal/repo"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func ValidateWebhook(ctx iris.Context) (repo.ConnectionEVO, repo.EventEVO, []byte, error) {
	webhookId := ctx.Params().Get("webhookId")
	_, err := uuid.Parse(webhookId)
	if webhookId == "" || err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return repo.ConnectionEVO{}, repo.EventEVO{}, nil, err
	}
	connection, err := repo.GetConnectionByWebhook(webhookId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo.ConnectionEVO{}, repo.EventEVO{}, nil, err
	}
	if connection.Status != "connected" {
		ctx.StopWithStatus(iris.StatusLocked)
	}
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo.ConnectionEVO{}, repo.EventEVO{}, nil, err
	}
	var event repo.EventEVO
	err = json.Unmarshal(body, &event)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return repo.ConnectionEVO{}, repo.EventEVO{}, nil, err
	}
	if event.InstanceID != connection.InstanceID {
		//fmt.Println(e[0].Body.InstanceToken, connection.InstanceID)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo.ConnectionEVO{}, repo.EventEVO{}, nil, err
	}
	return connection, event, body, nil
}
