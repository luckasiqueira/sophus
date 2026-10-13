package controllers

import (
	"errors"
	"log"
	"strings"
	"time"

	"sophus/internal/flowengine"
	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/web/components"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

type operatorMediaContext struct {
	Agent        repo.Agent
	Conversation repo.Conversation
	Connection   repo.ConnectionEVO
	Settings     repo.CompanyOperationSettings
}

func ListConversationMedia(ctx iris.Context) {
	operation, ok := loadOperatorMediaContext(ctx)
	if !ok {
		return
	}
	items, err := media.List(operation.Agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível carregar a galeria"})
		return
	}
	filtered := make([]media.Item, 0, len(items))
	for _, item := range items {
		if operatorMediaAllowed(item.Kind, item.SizeBytes, operation.Settings) {
			filtered = append(filtered, item)
		}
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(iris.Map{"data": iris.Map{
		"items":        filtered,
		"allowedTypes": operation.Settings.AllowedAttachmentTypes,
		"maxSizeMB":    operation.Settings.MaxAttachmentMB,
	}})
}

func UploadConversationMedia(ctx iris.Context) {
	operation, ok := loadOperatorMediaContext(ctx)
	if !ok {
		return
	}
	kind := strings.TrimSpace(ctx.FormValue("kind"))
	caption := strings.TrimSpace(ctx.FormValue("caption"))
	if len([]rune(caption)) > 1000 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "a legenda deve ter até 1000 caracteres"})
		return
	}
	file, header, err := ctx.FormFile("file")
	if err != nil || header == nil || header.Size <= 0 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "selecione um arquivo válido"})
		return
	}
	defer file.Close()
	if !operatorMediaAllowed(kind, header.Size, operation.Settings) {
		ctx.StopWithJSON(iris.StatusForbidden, iris.Map{"error": "este arquivo não atende à política de anexos da empresa"})
		return
	}
	reserved := header.Size
	if err := repo.ReserveCompanyStorage(operation.Agent.CompanyId, reserved); err != nil {
		if errors.Is(err, repo.ErrStorageLimitReached) {
			ctx.StopWithJSON(iris.StatusInsufficientStorage, iris.Map{"error": "o limite de armazenamento da empresa foi atingido"})
			return
		}
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível reservar espaço para o arquivo"})
		return
	}
	stored, err := media.SaveScoped(operation.Agent.CompanyId, media.ScopeMessages, kind, header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		_ = repo.ReleaseCompanyStorageReservation(operation.Agent.CompanyId, reserved)
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.CompleteCompanyStorageReservation(operation.Agent.CompanyId, reserved, stored.Size); err != nil {
		_, _ = media.Delete(operation.Agent.CompanyId, media.ScopeMessages, stored.Path)
		_ = repo.ReleaseCompanyStorageReservation(operation.Agent.CompanyId, reserved)
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível contabilizar o arquivo"})
		return
	}
	item, err := media.Inspect(operation.Agent.CompanyId, media.ScopeMessages, stored.Path)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível preparar o arquivo"})
		return
	}
	// Preserve the validated upload type; an audio WebM container must never be
	// sent as video merely because the operating system associates its suffix.
	item.Kind, item.MimeType = kind, stored.MimeType
	if !sendConversationMedia(ctx, operation, item, caption, stored.Name) {
		return
	}
}

func SendConversationGalleryMedia(ctx iris.Context) {
	operation, ok := loadOperatorMediaContext(ctx)
	if !ok {
		return
	}
	var body struct {
		Scope   string `json:"scope"`
		File    string `json:"file"`
		Caption string `json:"caption"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "dados da mídia inválidos"})
		return
	}
	body.Caption = strings.TrimSpace(body.Caption)
	if len([]rune(body.Caption)) > 1000 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "a legenda deve ter até 1000 caracteres"})
		return
	}
	item, err := media.Inspect(operation.Agent.CompanyId, body.Scope, body.File)
	if err != nil {
		ctx.StopWithJSON(iris.StatusNotFound, iris.Map{"error": "mídia não encontrada"})
		return
	}
	if !operatorMediaAllowed(item.Kind, item.SizeBytes, operation.Settings) {
		ctx.StopWithJSON(iris.StatusForbidden, iris.Map{"error": "esta mídia não atende à política de anexos da empresa"})
		return
	}
	if !sendConversationMedia(ctx, operation, item, body.Caption, item.Name) {
		return
	}
}

func loadOperatorMediaContext(ctx iris.Context) (operatorMediaContext, bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return operatorMediaContext{}, false
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return operatorMediaContext{}, false
	}
	conversation, err := repo.GetVisibleConversationByURL(conversationURL, agent)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return operatorMediaContext{}, false
	}
	assigned := conversation.AgentID != nil && *conversation.AgentID == agent.Id
	if conversation.AgentID == nil || (!assigned && !agent.IsAdmin()) || conversation.Status == repo.ConversationStatusClosed || conversation.Status == repo.ConversationStatusRunning {
		ctx.StopWithStatus(iris.StatusForbidden)
		return operatorMediaContext{}, false
	}
	connection, err := repo.GetConnectionByConversation(conversation.Id, agent.CompanyId)
	if err != nil || connection.Status != repo.ConnectionStatusConnected {
		ctx.StopWithStatus(iris.StatusConflict)
		return operatorMediaContext{}, false
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return operatorMediaContext{}, false
	}
	return operatorMediaContext{Agent: agent, Conversation: conversation, Connection: connection, Settings: settings}, true
}

func operatorMediaAllowed(kind string, size int64, settings repo.CompanyOperationSettings) bool {
	if size <= 0 || media.MaxSize(kind) <= 0 || size > media.MaxSize(kind) || size > int64(settings.MaxAttachmentMB)<<20 {
		return false
	}
	for _, allowed := range settings.AllowedAttachmentTypes {
		if kind == allowed {
			return true
		}
	}
	return false
}

func sendConversationMedia(ctx iris.Context, operation operatorMediaContext, item media.Item, caption, fileName string) bool {
	mediaURL, err := media.ScopedSignedURL(operation.Agent.CompanyId, item.Scope, item.Name, 15*time.Minute)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível preparar a mídia para envio"})
		return false
	}
	messageID, err := flowengine.NewChatMessenger(operation.Connection, operation.Settings).SendStoredMediaMessage(
		operation.Conversation, item.Kind, mediaURL, caption, fileName, item.Scope, item.Name)
	if err != nil {
		log.Printf("conversation media send failed: company=%d conversation=%d kind=%s file=%s error=%v", operation.Agent.CompanyId, operation.Conversation.Id, item.Kind, item.Name, err)
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": "não foi possível enviar a mídia; o arquivo permanece disponível na galeria"})
		return false
	}
	message, err := repo.GetMessageForConversation(messageID, operation.Conversation.Id, operation.Agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "a mídia foi enviada, mas não foi possível atualizar a conversa"})
		return false
	}
	message.Timezone = operation.Settings.Timezone
	if operation.Agent.IsSupport {
		recordCompanyAction(ctx, operation.Agent, "send_media", "message", messageID)
	}
	sse.NotifyConversations(operation.Agent.CompanyId)
	ctx.RenderComponent(components.MessageSent(message, operation.Agent.CompanyId, operation.Conversation.URL.String()))
	return true
}
