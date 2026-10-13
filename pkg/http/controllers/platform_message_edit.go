package controllers

import (
	"database/sql"
	"errors"
	"strings"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func EditConversationMessage(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	u, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	var body struct {
		MessageID    string  `json:"messageId"`
		Text         string  `json:"text"`
		ExpectedText *string `json:"expectedText"`
	}
	if err := ctx.ReadJSON(&body); err != nil || strings.TrimSpace(body.MessageID) == "" || strings.TrimSpace(body.Text) == "" || len(body.Text) > 65536 || body.ExpectedText == nil {
		writeJSONError(ctx, iris.StatusBadRequest, "informe a mensagem e um texto válido para editar")
		return
	}
	conversation, err := repo.GetVisibleConversationByURL(u, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	_, err = repo.EditPlatformMessage(agent, u, body.MessageID, body.Text, *body.ExpectedText)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrMessageEditForbidden):
			writeJSONError(ctx, iris.StatusForbidden, err.Error())
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "mensagem não encontrada neste atendimento")
		case errors.Is(err, repo.ErrMessageNotEditable), errors.Is(err, repo.ErrMessageEditConflict), errors.Is(err, repo.ErrEditConnectionUnavailable):
			writeJSONError(ctx, iris.StatusConflict, err.Error())
		case errors.Is(err, repo.ErrWhatsAppEditRejected):
			writeJSONError(ctx, iris.StatusBadGateway, err.Error())
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível concluir a edição da mensagem")
		}
		return
	}
	stored, err := repo.GetMessageForConversation(body.MessageID, conversation.Id, agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "edição enviada; recarregue a conversa para conferir")
		return
	}
	connection, err := repo.GetConnectionByConversation(conversation.Id, agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "edição enviada; recarregue a conversa para conferir")
		return
	}
	html, err := updatedWebhookMessageHTML(stored, conversation, connection)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "edição enviada; recarregue a conversa para conferir")
		return
	}
	sse.Global.SendEvent(conversation.URL.String(), "message-update", html)
	sse.NotifyConversations(agent.CompanyId)
	ctx.JSON(iris.Map{"data": iris.Map{"messageId": body.MessageID, "text": stored.Text, "html": html}})
}
