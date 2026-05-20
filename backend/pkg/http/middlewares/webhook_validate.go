package middlewares

import (
	"zubly/backend/internal/database"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func ValidateWebhook(ctx iris.Context) {
	webhookId := ctx.URLParam("webhookId")
	_, err := uuid.Parse(webhookId)
	if webhookId == "" || err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	connection, err := database.GetConnectionByWebhook(webhookId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
	if connection.Status != "connected" {
		ctx.StopWithStatus(iris.StatusLocked)
	}
	ctx.Next()
}
