package controllers

import (
	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"
)

func requestedContactHistory(ctx iris.Context, conversation repo.Conversation) (bool, bool) {
	switch ctx.URLParam("history") {
	case "", "current":
		return false, true
	case "contact":
		if conversation.ConnectionID <= 0 {
			writeJSONError(ctx, iris.StatusBadRequest, "o histórico do contato requer a conexão original do atendimento")
			return false, false
		}
		return true, true
	default:
		writeJSONError(ctx, iris.StatusBadRequest, "escopo de histórico inválido")
		return false, false
	}
}

func loadHistoryScope(conversation repo.Conversation, agent repo.Agent, before int, contact bool) (repo.ConversationMessagePage, []repo.ConversationEvent, error) {
	if contact {
		page, err := repo.GetContactHistoryPage(conversation.Id, agent.CompanyId, before)
		if err != nil {
			return page, nil, err
		}
		events, err := repo.GetContactHistoryEvents(conversation.Id, agent.CompanyId, page)
		return page, events, err
	}
	page, err := repo.GetConversationMessagePage(conversation.Id, agent.CompanyId, before)
	if err != nil {
		return page, nil, err
	}
	if before == 0 {
		events, err := repo.GetConversationEventsWindow(conversation.Id, agent.CompanyId, page.WindowAfter, nil)
		return page, events, err
	}
	events, err := repo.GetCachedConversationEventsWindow(conversation.Id, agent.CompanyId, page.WindowAfter, page.WindowBefore)
	return page, events, err
}

// Only replace the transcript: switching history scopes preserves the current
// composer, draft, notes editor, SSE connection and assignment controls.
func ConversationTimeline(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	u, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	conversation, err := repo.GetVisibleConversationByURL(u, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	contact, ok := requestedContactHistory(ctx, conversation)
	if !ok {
		return
	}
	page, events, err := loadHistoryScope(conversation, agent, 0, contact)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar o histórico")
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar as configurações")
		return
	}
	for index := range page.Messages {
		page.Messages[index].Timezone = settings.Timezone
	}
	ctx.Header("Cache-Control", "private, no-store")
	setMessageMediaRetryURL(page.Messages, conversation.URL.String())
	ctx.RenderComponent(web.ConversationTimelineContent(conversation, agent, page.Messages, events, page))
}
