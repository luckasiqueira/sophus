package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"zubly/backend/internal/database"
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
	apiToken, status := database.CheckAPIToken(ctx.GetHeader("apitoken"))
	if status != iris.StatusOK {
		ctx.StopWithStatus(status)
	}
	err = json.Unmarshal(body, &m)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	m.MessageBase.Id = uuid.NewString()
	status, err = m.Send(apiToken)
	if err != nil {
		ctx.StopWithStatus(status)
	}
	fmt.Println(m)
}
