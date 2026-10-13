package controllers

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web/components"
)

func RetryConversationMedia(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	u, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, 400, "conversa inválida")
		return
	}
	id, err := strconv.Atoi(ctx.FormValue("id"))
	if err != nil || id <= 0 {
		writeJSONError(ctx, 400, "mensagem inválida")
		return
	}
	anchor, err := repo.GetVisibleConversationByURL(u, agent)
	if err != nil {
		writeJSONError(ctx, 403, "você não tem acesso a esta conversa")
		return
	}
	stored, message, err := repo.GetMessageMediaRecovery(anchor.Id, agent.CompanyId, id)
	if err != nil || stored.IsDeleted || stored.MediaType == "view_once" {
		writeJSONError(ctx, 404, "mídia indisponível nesta conversa")
		return
	}
	connection, err := repo.GetConnectionByConversation(stored.ConversationId, agent.CompanyId)
	if err != nil || connection.Status != repo.ConnectionStatusConnected {
		writeJSONError(ctx, 409, "reconecte o WhatsApp para recuperar esta mídia")
		return
	}
	if stored.MediaPath == "" {
		if err := repo.LoadLegacyMediaSource(&message, connection); err != nil {
			writeJSONError(ctx, 409, err.Error())
			return
		}
		if err := completeExistingWebhookMedia(message, connection); err != nil {
			if errors.Is(err, repo.ErrMediaSourceUnavailable) || errors.Is(err, repo.ErrStorageLimitReached) {
				writeJSONError(ctx, 409, err.Error())
			} else {
				writeJSONError(ctx, 502, "não foi possível recuperar o arquivo no WhatsApp; tente novamente")
			}
			return
		}
	}
	stored, err = repo.GetMessageForConversation(stored.MessageId, stored.ConversationId, agent.CompanyId)
	if err == nil && stored.MediaPath == "" {
		var display repo.MessageDisplay
		_ = json.Unmarshal(stored.DisplayData, &display)
		if display.MediaUnavailableReason == "storage_quota_exceeded" {
			writeJSONError(ctx, 507, "limite de armazenamento atingido; libere espaço ou aumente o plano antes de recuperar esta mídia")
			return
		}
	}
	if err != nil || stored.MediaPath == "" {
		writeJSONError(ctx, 409, repo.ErrMediaSourceUnavailable.Error())
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, 500, "não foi possível consultar as configurações")
		return
	}
	stored.Timezone = settings.Timezone
	html := ""
	actionURL := ""
	if stored.ConversationId == anchor.Id {
		actionURL = anchor.URL.String()
	}
	if stored.IsFromMe {
		html, err = renderComponent(components.MessageSent(stored, agent.CompanyId, actionURL))
	} else {
		html, err = renderComponent(components.MessageReceived(stored, agent.CompanyId, anchor.Contact.Id, actionURL))
	}
	if err != nil {
		writeJSONError(ctx, 500, "não foi possível exibir a mídia")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"html": html})
}

func setMessageMediaRetryURL(messages []repo.MessageData, url string) {
	for index := range messages {
		message := &messages[index]
		if message.Id > 0 && !message.IsDeleted && message.MediaPath == "" {
			switch message.MediaType {
			case "image", "video", "audio", "document", "sticker":
				message.MediaRetryURL = "/messages/" + url + "/media/retry"
			}
		}
	}
}
