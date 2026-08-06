package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils"
	"sophus/web"
	"sophus/web/components"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Messages(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	tab := ctx.URLParamDefault("tab", "active")
	if tab != "active" && tab != "pending" && tab != "closed" {
		tab = "active"
	}
	conversations, err := repo.GetConversationsByAgent(agent, tab)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.RenderComponent(web.Messages(conversations, tab))
}

func MessageOpen(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	u := ctx.Params().Get("url")
	url, err := uuid.Parse(u)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	activeConversation, err := repo.GetVisibleConversationByURL(url, agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	tab := conversationTab(activeConversation)
	conversations, err := repo.GetConversationsByAgent(agent, tab)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	messages, err := repo.GetMessagesByConversationURL(url)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	ctx.RenderComponent(web.MessageSingle(activeConversation, agent, conversations, messages))
}

func conversationTab(conversation repo.Conversation) string {
	if conversation.Status == repo.ConversationStatusClosed {
		return "closed"
	}
	if conversation.AgentID == nil {
		return "pending"
	}
	return "active"
}

func AcceptConversation(ctx iris.Context) {
	updatePendingConversation(ctx, true)
}

func IgnoreConversation(ctx iris.Context) {
	updatePendingConversation(ctx, false)
}

func updatePendingConversation(ctx iris.Context, accept bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	var updated bool
	if accept {
		updated, err = repo.AcceptConversation(conversationURL, agent)
	} else {
		updated, err = repo.IgnoreConversation(conversationURL, agent)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if !updated {
		ctx.StopWithStatus(iris.StatusConflict)
		return
	}
	if accept {
		ctx.Header("HX-Redirect", "/messages/"+conversationURL.String())
	} else {
		ctx.Header("HX-Redirect", "/messages?tab=pending")
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func CloseConversation(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if err := repo.CloseConversationByURL(conversationURL, agent.CompanyId, agent.Id, agent.IsAdmin()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.Header("HX-Redirect", "/messages")
	ctx.StatusCode(iris.StatusNoContent)
}

func SendMessage(ctx iris.Context) {
	connection := repo.ConnectionEVO{}
	msg := repo.TextMessageEVO{}
	var err error
	serveHX := !middlewares.IsAPIAuthentication(ctx)
	if !serveHX {
		connection, msg, err = sendMessageAPI(ctx)
	} else {
		connection, msg, err = sendMessageJWT(ctx)
	}
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	status, fullJson, err := msg.Send(connection.EvolutionAPIKey()) // coletar a resposta, pra puxar o data e o messageid e salvar corretamente no banco de dados
	if err != nil || status != 200 {
		if status < 100 {
			status = iris.StatusBadGateway
		}
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
	connection, err := repo.GetConnectionByAPI(apiToken)
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
	urlID := ctx.FormValue("conversation")
	if urlID == "" {
		parts := strings.Split(ctx.Request().Header.Get("Referer"), "messages/")
		if len(parts) != 2 {
			return repo.ConnectionEVO{}, repo.TextMessageEVO{}, errors.New("conversation is required")
		}
		urlID = parts[1]
	}
	url, err := uuid.Parse(urlID)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	conversation, err := repo.GetVisibleConversationByURL(url, agent)
	if err != nil {
		return repo.ConnectionEVO{}, repo.TextMessageEVO{}, err
	}
	assignedToAgent := conversation.AgentID != nil && *conversation.AgentID == agent.Id
	if !assignedToAgent && !agent.IsAdmin() {
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
