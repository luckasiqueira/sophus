package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils"
	"sophus/web"
	"sophus/web/components"
	"strings"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Messages(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
	}
	conversations, err := repo.GetConversationsByAgent(agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
	ctx.RenderComponent(web.Messages(conversations))
}

func MessageOpen(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
	}
	conversations, err := repo.GetConversationsByAgent(agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
	}
	// NOTE: validate if agent.CompanyId can open this
	u := ctx.Params().Get("url")
	url, err := uuid.Parse(u)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	messages, err := repo.GetMessagesByConversationURL(url)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	if len(messages) == 0 {
		ctx.StopWithStatus(iris.StatusNoContent)
		return
	}

	activeConversation := repo.Conversation{}
	agentCanSeeThisConversation := false
	for _, c := range conversations {
		if c.URL.String() == u {
			agentCanSeeThisConversation = true
			activeConversation = c
		}
	}

	if !agentCanSeeThisConversation {
		// NOTE: render a page to says that user has no permissions to see that conversation
		fmt.Println(err, agent)
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	ctx.RenderComponent(web.MessageSingle(activeConversation, agent.CompanyId, conversations, messages))
}

func SendMessage(ctx iris.Context) {
	connection := repo.ConnectionEVO{}
	msg := repo.TextMessageEVO{}
	var err error
	if ctx.GetHeader("apitoken") != "" {
		connection, msg, err = sendMessageAPI(ctx)
	}
	var serveHX bool
	if middlewares.IsValidJWT(ctx) {
		serveHX = true
		connection, msg, err = sendMessageJWT(ctx)
	}
	status, fullJson, err := msg.Send(connection.ConnectionKey.String()) // coletar a resposta, pra puxar o data e o messageid e salvar corretamente no banco de dados
	if err != nil || status != 200 {
		ctx.StopWithStatus(status)
		return
	}
	msg.JSON = fullJson
	err = repo.SaveEvoMessage(msg, connection)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if serveHX {
		var jsonMsg repo.EventMessageEVO
		err = json.Unmarshal(fullJson, &jsonMsg)
		t, err := utils.TimeFromTimestamp(jsonMsg.Data.Info.Timestamp)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		m := repo.MessageData{
			Text:     msg.Text,
			QuotedId: jsonMsg.Data.Message.TXT.ContextInfo.QuotedMessage.Text,
			//MediaType:      "",
			CreatedAt: t,
			UpdatedAt: t,
			MediaPath: "",
		}
		defer ctx.RenderComponent(components.MessageSent(m, connection.CompanyID))
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
	msg.Number = contact.Number
	return connection, msg, nil
}
