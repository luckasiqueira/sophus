package controllers

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"
)

func SearchConversation(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	url, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	query := strings.TrimSpace(ctx.URLParam("q"))
	if query == "" || utf8.RuneCountInString(query) > 1000 {
		writeJSONError(ctx, iris.StatusBadRequest, "informe uma busca de até 1.000 caracteres")
		return
	}
	offset := 0
	if value := ctx.URLParam("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 1000000 {
			writeJSONError(ctx, iris.StatusBadRequest, "posição de busca inválida")
			return
		}
	}
	conversation, err := repo.GetVisibleConversationByURL(url, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	contact, ok := requestedContactHistory(ctx, conversation)
	if !ok {
		return
	}
	var result repo.ConversationSearchResults
	if contact {
		result, err = repo.SearchContactHistoryMessages(conversation.Id, agent.CompanyId, query, offset)
	} else {
		result, err = repo.SearchConversationMessages(agent.CompanyId, conversation.Id, query, offset)
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível pesquisar as mensagens")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(result)
}

func ConversationSearchContext(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	url, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	messageID, err := strconv.Atoi(ctx.URLParam("id"))
	if err != nil || messageID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "mensagem inválida")
		return
	}
	conversation, err := repo.GetVisibleConversationByURL(url, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	contact, ok := requestedContactHistory(ctx, conversation)
	if !ok {
		return
	}
	var messages []repo.MessageData
	var conversations []repo.Conversation
	if contact {
		messages, conversations, err = repo.GetContactHistoryContext(conversation.Id, agent.CompanyId, messageID)
	} else {
		messages, err = repo.GetConversationMessageContext(conversation.Id, agent.CompanyId, messageID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "esta mensagem não está mais disponível nesta conversa")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar o contexto da mensagem")
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar as configurações")
		return
	}
	for index := range messages {
		messages[index].Timezone = settings.Timezone
	}
	page := repo.ConversationMessagePage{Conversations: conversations}
	if contact {
		page.HistoryScope = "contact"
	}
	html, err := renderComponent(web.ConversationSearchPreview(conversation, messages, messageID, page))
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível exibir o contexto da mensagem")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"html": html})
}
