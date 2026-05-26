package controllers

import (
	"encoding/json"
	"fmt"
	"os"
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http/middlewares"
	"time"

	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	connection, event, body, err := middlewares.ValidateWebhook(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	saveBody(body)
	switch event.EventType {
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
		msg.FullJSON = body
		err = repo.SaveEvoMessage(msg, connection)
		if err != nil {
			fmt.Println(err)
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
	}
}

func saveBody(body []byte) {
	file := fmt.Sprintf("%s.txt", time.Now().Format("20060102150405"))
	if err := os.WriteFile(file, body, os.ModePerm); err != nil {
		fmt.Println(err)
	}
}
