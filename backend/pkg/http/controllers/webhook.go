package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"zubly/backend/pkg/wpp"

	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
	e := wpp.Event{}
	err = json.Unmarshal(body, &e)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	switch e[0].Body.EventType {
	case "QRCode":
		qrcode := wpp.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		fmt.Println(qrcode)
	case "Message":
		message := wpp.EventMessage{}
		err = json.Unmarshal(body, &message)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		fmt.Println(message)
	}
	fmt.Println(err)
}
