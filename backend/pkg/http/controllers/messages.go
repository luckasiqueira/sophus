package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"zubly/backend/pkg/wpp"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func SendMessage(ctx iris.Context) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	m := wpp.TextMessage{}
	apiToken := ctx.GetHeader("apitoken")
	err = json.Unmarshal(body, &m)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	m.MessageBase.Id = uuid.NewString()
	m.Send(apiToken)
	fmt.Println(apiToken)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	fmt.Println(m)
}
