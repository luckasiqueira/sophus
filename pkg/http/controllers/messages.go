package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/web"
	"sophus/web/components"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Messages(ctx iris.Context) {
	if isConversationFragment(ctx) {
		ctx.RenderComponent(web.EmptyConversationPanel())
		return
	}
	data, ok := conversationListData(ctx)
	if !ok {
		return
	}
	ctx.RenderComponent(web.Messages(data.Conversations, data.Tab, data.Scope, data.Counts))
}

func ConversationList(ctx iris.Context) {
	data, ok := conversationListData(ctx)
	if !ok {
		return
	}
	ctx.Header("Cache-Control", "no-store")
	selected := strings.TrimSpace(ctx.URLParam("selected"))
	if _, err := uuid.Parse(selected); err != nil {
		selected = ""
	}
	ctx.RenderComponent(web.ConversationSidebar(data.Conversations, data.Tab, data.Scope, data.Counts, selected))
}

type conversationListView struct {
	Tab           string
	Scope         string
	Conversations []repo.Conversation
	Counts        repo.ConversationScopeCounts
}

func conversationListData(ctx iris.Context) (conversationListView, bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return conversationListView{}, false
	}
	tab := ctx.URLParamDefault("tab", "active")
	if tab != "active" && tab != "pending" && tab != "closed" {
		tab = "active"
	}
	scope := conversationScope(ctx.URLParam("scope"))
	listStarted := time.Now()
	conversations, err := repo.GetConversationsByAgent(agent, tab, scope == "group")
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return conversationListView{}, false
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return conversationListView{}, false
	}
	for index := range conversations {
		conversations[index].Timezone = settings.Timezone
	}
	middlewares.ServerTiming(ctx, "conversations", listStarted)
	countsStarted := time.Now()
	counts, err := repo.GetConversationScopeCounts(agent, scope == "group")
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return conversationListView{}, false
	}
	middlewares.ServerTiming(ctx, "counts", countsStarted)
	return conversationListView{Tab: tab, Scope: scope, Conversations: conversations, Counts: counts}, true
}

func conversationScope(value string) string {
	if value == "group" {
		return "group"
	}
	return "individual"
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
	contactHistory, ok := requestedContactHistory(ctx, activeConversation)
	if !ok {
		return
	}
	marked, err := repo.MarkConversationRead(activeConversation.Id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if marked > 0 {
		sse.NotifyConversations(agent.CompanyId)
	}
	tab := conversationTab(activeConversation)
	scope := "individual"
	if activeConversation.Contact.IsGroup {
		scope = "group"
	}
	historyStarted := time.Now()
	page, events, err := loadHistoryScope(activeConversation, agent, 0, contactHistory)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	messages := page.Messages
	setMessageMediaRetryURL(messages, activeConversation.URL.String())
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	activeConversation.CanEditMessages = repo.AgentCanEditMessages(settings.MessageEditPolicy, agent, activeConversation)
	for index := range messages {
		messages[index].Timezone = settings.Timezone
	}
	middlewares.ServerTiming(ctx, "history", historyStarted)
	ctx.Header("Cache-Control", "private, no-store")
	if isConversationFragment(ctx) {
		ctx.RenderComponent(web.ConversationPanel(activeConversation, agent, messages, events, page))
		return
	}
	conversations, err := repo.GetConversationsByAgent(agent, tab, scope == "group")
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	counts, err := repo.GetConversationScopeCounts(agent, scope == "group")
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.RenderComponent(web.MessageSingle(activeConversation, agent, conversations, messages, events, counts, page))
}

func isConversationFragment(ctx iris.Context) bool {
	return strings.EqualFold(ctx.GetHeader("HX-Request"), "true") && !strings.EqualFold(ctx.GetHeader("HX-History-Restore-Request"), "true")
}

func ConversationHistory(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	u, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	before, err := strconv.Atoi(ctx.URLParam("before"))
	if err != nil || before <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "posição do histórico inválida")
		return
	}
	conversation, err := repo.GetVisibleConversationByURL(u, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	contactHistory, ok := requestedContactHistory(ctx, conversation)
	if !ok {
		return
	}
	page, events, err := loadHistoryScope(conversation, agent, before, contactHistory)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "não foi possível carregar o histórico")
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
	setMessageMediaRetryURL(page.Messages, conversation.URL.String())
	html, err := renderComponent(web.ConversationMessages(conversation, agent, page.Messages, events, page))
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível exibir o histórico")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"html": html, "before": page.BeforeID, "hasOlder": page.HasOlder})
}

func MarkConversationRead(ctx iris.Context) {
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
	conversation, err := repo.GetVisibleConversationByURL(conversationURL, agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	marked, err := repo.MarkConversationRead(conversation.Id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if marked > 0 {
		sse.NotifyConversations(agent.CompanyId)
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func ConversationNotification(ctx iris.Context) {
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
	conversation, err := repo.GetVisibleConversationByURL(conversationURL, agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	message, err := repo.GetMessageForConversation(strings.TrimSpace(ctx.URLParam("message")), conversation.Id, agent.CompanyId)
	if err != nil || message.IsFromMe {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	preview := strings.TrimSpace(message.Text)
	if preview == "" {
		switch message.MediaType {
		case "image":
			preview = "Imagem recebida"
		case "video":
			preview = "Vídeo recebido"
		case "audio":
			preview = "Áudio recebido"
		case "document":
			preview = "Documento recebido"
		default:
			preview = "Nova mensagem"
		}
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(iris.Map{"data": iris.Map{
		"conversation":    conversation.URL.String(),
		"contact":         conversation.Contact.Name,
		"department":      conversation.DepartmentName,
		"departmentColor": conversation.DepartmentColor,
		"isGroup":         conversation.Contact.IsGroup,
		"preview":         preview,
		"messageId":       message.MessageId,
	}})
}

func conversationTab(conversation repo.Conversation) string {
	if conversation.Status == repo.ConversationStatusClosed {
		return "closed"
	}
	if conversation.Status == repo.ConversationStatusRunning {
		return "pending"
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
	sse.NotifyConversations(agent.CompanyId)
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
	sse.NotifyConversations(agent.CompanyId)
	ctx.StatusCode(iris.StatusNoContent)
}

type conversationTransferInput struct {
	AgentID      *int `json:"agentId"`
	DepartmentID *int `json:"departmentId"`
}

func ConversationTransferOptions(ctx iris.Context) {
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
	if _, err := repo.GetVisibleConversationByURL(conversationURL, agent); err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	departments, err := repo.GetDepartmentsByCompany(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os departamentos")
		return
	}
	agents, err := repo.GetAgentAssignmentOptions(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os operadores")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"departments": departments, "agents": agents}})
}

func TransferConversation(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	var input conversationTransferInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "dados de transferência inválidos")
		return
	}
	if err := repo.TransferConversationByURL(conversationURL, agent.CompanyId, agent.Id, agent.IsAdmin(), input.AgentID, input.DepartmentID); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "conversa não encontrada ou indisponível para transferência")
		case errors.Is(err, repo.ErrInvalidConversationTransferTarget):
			writeJSONError(ctx, iris.StatusBadRequest, "selecione um departamento válido para o operador escolhido")
		default:
			log.Printf("conversation transfer failed: company=%d actor=%d conversation=%s error=%v", agent.CompanyId, agent.Id, conversationURL, err)
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível transferir a conversa")
		}
		return
	}
	sse.NotifyConversations(agent.CompanyId)
	ctx.StatusCode(iris.StatusNoContent)
}

func SendMessage(ctx iris.Context) {
	if ctx.FormValue("internalNote") == "true" {
		writeJSONError(ctx, iris.StatusBadRequest, "notas internas devem ser salvas na conversa, sem envio ao WhatsApp")
		return
	}
	connection := repo.ConnectionEVO{}
	msg := repo.TextMessageEVO{}
	var operation chatTextContext
	var err error
	serveHX := !middlewares.IsAPIAuthentication(ctx)
	if !serveHX {
		connection, msg, err = sendMessageAPI(ctx)
	} else {
		operation, msg, err = sendMessageJWT(ctx)
		connection = operation.Connection
	}
	if err != nil {
		log.Printf("message send request rejected: api_auth=%t error=%v", !serveHX, err)
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	settings, err := repo.GetCompanyOperationSettings(connection.CompanyID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if serveHX {
		agent := operation.Agent
		preferences, preferenceErr := requestAgentPreferences(ctx, agent)
		if preferenceErr != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		msg.Text = preferences.SignMessage(msg.Text, agent.Name)
	}
	if serveHX {
		msg.Delay = 0
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
	var messageID string
	if serveHX {
		messageID, err = repo.SaveChatTextMessage(msg, connection, operation.Conversation)
	} else {
		err = repo.SaveEvoMessage(msg, connection)
	}
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	sse.NotifyConversations(connection.CompanyID)
	if serveHX {
		if operation.Agent.IsSupport {
			recordCompanyAction(ctx, operation.Agent, "send_message", "message", messageID)
		}
		conversation := operation.Conversation
		// Closed conversations retain the existing behavior of creating a new
		// attendance. Active chats use the already authorized conversation.
		if conversation.Status == repo.ConversationStatusClosed || conversation.Status == "resolved" {
			conversation, err = repo.GetConversationByMessage(messageID, connection.CompanyID, connection.Id)
			if err != nil {
				ctx.StopWithStatus(iris.StatusInternalServerError)
				return
			}
		}
		m, err := repo.GetMessageForConversation(messageID, conversation.Id, connection.CompanyID)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		m.Timezone = settings.Timezone
		defer ctx.RenderComponent(components.MessageSent(m, connection.CompanyID, conversation.URL.String()))
	}
}

func ReactToMessage(ctx iris.Context) {
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
	conversation, err := repo.GetVisibleConversationByURL(conversationURL, agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	assignedToAgent := conversation.AgentID != nil && *conversation.AgentID == agent.Id
	if (!assignedToAgent && !agent.IsAdmin()) || conversation.Status == repo.ConversationStatusClosed || conversation.Status == repo.ConversationStatusRunning {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	messageID := strings.TrimSpace(ctx.FormValue("messageId"))
	reaction := strings.TrimSpace(ctx.FormValue("reaction"))
	if messageID == "" || reaction == "" || utf8.RuneCountInString(reaction) > 32 {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	target, err := repo.GetMessageForConversation(messageID, conversation.Id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	remove := false
	if current, currentErr := repo.OwnMessageReaction(messageID, conversation.Id, agent.CompanyId); currentErr == nil && current == reaction {
		remove = true
	} else if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	number := contact.Number
	if contact.IsGroup {
		number = contact.JID
	}
	payload := repo.ReactionMessageEVO{
		Number:   number,
		Reaction: reaction,
		ID:       target.MessageId,
		FromMe:   target.IsFromMe,
	}
	if target.IsGroup && !target.IsFromMe {
		if strings.TrimSpace(target.Sender) == "" {
			ctx.StopWithStatus(iris.StatusConflict)
			return
		}
		payload.Participant = target.Sender
	}
	if remove {
		payload.Reaction = "remove"
	}
	connection, err := repo.GetConnectionByConversation(conversation.Id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	status, _, err := payload.Send(connection.EvolutionAPIKey())
	if err != nil || status < iris.StatusOK || status >= iris.StatusMultipleChoices {
		ctx.StopWithStatus(iris.StatusBadGateway)
		return
	}
	storedReaction := reaction
	if remove {
		storedReaction = ""
	}
	updated, err := repo.ApplyMessageReaction(messageID, conversation.Id, agent.CompanyId, "self", storedReaction, "", true)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.RenderComponent(components.MessageReactions(updated, conversation.URL.String(), false))
	if html, renderErr := renderComponent(components.MessageReactions(updated, conversation.URL.String(), true)); renderErr == nil {
		sse.Global.Send(conversation.URL.String(), html)
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

type chatTextContext struct {
	Agent        repo.Agent
	Connection   repo.ConnectionEVO
	Conversation repo.Conversation
}

func sendMessageJWT(ctx iris.Context) (chatTextContext, repo.TextMessageEVO, error) {
	msg := repo.TextMessageEVO{}
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return chatTextContext{}, repo.TextMessageEVO{}, err
	}
	urlID := ctx.FormValue("conversation")
	if urlID == "" {
		parts := strings.Split(ctx.Request().Header.Get("Referer"), "messages/")
		if len(parts) != 2 {
			return chatTextContext{}, repo.TextMessageEVO{}, errors.New("conversation is required")
		}
		urlID = parts[1]
	}
	url, err := uuid.Parse(urlID)
	if err != nil {
		return chatTextContext{}, repo.TextMessageEVO{}, err
	}
	conversation, err := repo.GetVisibleConversationByURL(url, agent)
	if err != nil {
		return chatTextContext{}, repo.TextMessageEVO{}, err
	}
	assignedToAgent := conversation.AgentID != nil && *conversation.AgentID == agent.Id
	if !assignedToAgent && !agent.IsAdmin() {
		return chatTextContext{}, repo.TextMessageEVO{}, errors.New("agent not authorized in this conversation")
	}
	// wait for the allowedConnections slice on agents table to check if agent has permission to that connection
	connection, err := repo.GetConnectionByConversation(conversation.Id, agent.CompanyId)
	if err != nil {
		return chatTextContext{}, repo.TextMessageEVO{}, err
	}
	contact := conversation.Contact
	msg.Text = strings.TrimSpace(ctx.FormValue("message"))
	if msg.Text == "" {
		return chatTextContext{}, repo.TextMessageEVO{}, errors.New("message is required")
	}
	msg.Number = contact.Number
	if contact.IsGroup {
		msg.Number = contact.JID
	}
	quotedID := strings.TrimSpace(ctx.FormValue("quotedId"))
	if quotedID != "" {
		quoted, quoteErr := repo.GetMessageForConversation(quotedID, conversation.Id, agent.CompanyId)
		if quoteErr != nil {
			return chatTextContext{}, repo.TextMessageEVO{}, quoteErr
		}
		msg.MessageID = quoted.MessageId
		msg.Participant = quotedMessageParticipant(quoted, contact, connection)
		if msg.Participant == "" {
			return chatTextContext{}, repo.TextMessageEVO{}, errors.New("quoted message participant is unavailable")
		}
	}
	return chatTextContext{Agent: agent, Connection: connection, Conversation: conversation}, msg, nil
}

func quotedMessageParticipant(quoted repo.MessageData, contact repo.Contact, connection repo.ConnectionEVO) string {
	if quoted.IsFromMe {
		return repo.WhatsAppParticipant(connection.Number)
	}
	if quoted.IsGroup {
		return repo.WhatsAppParticipant(quoted.Sender)
	}
	return repo.WhatsAppParticipant(contact.JID, contact.Number, contact.LID)
}
