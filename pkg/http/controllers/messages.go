package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"strings"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func SendMessage(ctx iris.Context) {
	connection := repo.ConnectionEVO{}
	msg := repo.TextMessageEVO{}
	var err error
	if ctx.GetHeader("apitoken") != "" {
		connection, msg, err = sendMessageAPI(ctx)
	}
	if middlewares.IsValidJWT(ctx) {
		connection, msg, err = sendMessageJWT(ctx)
	}
	status, fullJson, err := msg.Send(connection.ConnectionKey.String()) // coletar a resposta, pra puxar o data e o messageid e salvar corretamente no banco de dados
	if err != nil || status != 200 {
		fmt.Println(err, string(fullJson))
		ctx.StopWithStatus(status)
		return
	}
	msg.JSON = fullJson
	err = repo.SaveEvoMessage(msg, connection)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
}

func sendMessageAPI(ctx iris.Context) (repo.ConnectionEVO, repo.TextMessageEVO, error) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	msg := repo.TextMessageEVO{}
	apiToken := ctx.GetHeader("apitoken")
	err = json.Unmarshal(body, &msg)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	connection, err := repo.GetConnectionByToken(apiToken)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	return connection, msg, err
}

func sendMessageJWT(ctx iris.Context) (repo.ConnectionEVO, repo.TextMessageEVO, error) {
	msg := repo.TextMessageEVO{}
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	urlID := strings.Split(ctx.Request().Header.Get("Referer"), "messages/")[1]
	url := uuid.MustParse(urlID)
	conversation, err := repo.GetConversationByURL(url)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	if conversation.AgentID != agent.Id || agent.Role != "admin" {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, errors.New("agent not authorized in this conversation")
	}
	// wait for the allowedConnections slice on agents table to check if agent has permission to that connection
	connection, err := repo.GetConnectionByConversationURL(url)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	msg.Text = ctx.FormValue("message")
	fmt.Println(conversation)
	msg.Number = contact.Number
	return connection, msg, nil
}
