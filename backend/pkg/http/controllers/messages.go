package controllers

import (
	"encoding/json"
	"io"
	"sophus/backend/internal/repo"

	"github.com/kataras/iris/v12"
)

func SendMessage(ctx iris.Context) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
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
	//msg.MessageBaseEVO.Id = uuid.NewString()
	status, fullJson, err := msg.Send(connection.ConnectionKey.String()) // coletar a resposta, pra puxar o data e o messageid e salvar corretamente no banco de dados
	if err != nil || status != 200 {
		ctx.StopWithStatus(status)
	}
	msg.JSON = fullJson
	err = repo.SaveEvoMessage(msg, connection)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
}
