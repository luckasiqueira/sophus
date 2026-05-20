package controllers

import (
	"encoding/json"
	"zubly/backend/internal/repo"
	"zubly/backend/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	connection, event, body, err := middlewares.ValidateWebhook(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	switch event[0].Body.EventType {
	case "QRCode":
		qrcode := repo.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
	case "Message":
		msg := repo.EventMessageEVO{}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		err = repo.SaveEvoMessage(msg, connection)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
	}
}
