package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"zubly/backend/internal/repo"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func SendMessage(ctx iris.Context) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	msg := repo.TextMessageEVO{}
	apiToken := ctx.GetHeader("apitoken")
	err = json.Unmarshal(body, &msg)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	connection, err := repo.GetConnectionByToken(apiToken)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	msg.MessageBaseEVO.Id = uuid.NewString()
	status, err := msg.Send(connection.ConnectionKey.String()) // coletar a resposta, pra puxar o data e o messageid e salvar corretamente no banco de dados
	if err != nil || status != 200 {
		ctx.StopWithStatus(status)
	}
	err = repo.MessageSaveAPI(apiToken, msg)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
}
