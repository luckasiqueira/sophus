package controllers

import (
	"encoding/json"
	"fmt"
	"zubly/backend/internal/repo"
	"zubly/backend/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	//body, err := io.ReadAll(ctx.Request().Body)
	//if err != nil {
	//	ctx.StopWithStatus(iris.StatusInternalServerError)
	//}
	//e := wpp.EventEVO{}
	//err = json.Unmarshal(body, &e)
	//if err != nil {
	//	ctx.StopWithStatus(iris.StatusBadRequest)
	//}
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
		message := repo.EventMessageEVO{}
		err = json.Unmarshal(body, &message)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		err = repo.MessageSave(connection, message)
		if err != nil {
			fmt.Println(err)
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
	}
}
