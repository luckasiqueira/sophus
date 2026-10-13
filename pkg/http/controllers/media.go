package controllers

import (
	"database/sql"
	"errors"
	"mime"
	"os"
	"strconv"
	"strings"
	"time"

	"sophus/internal/flowengine"
	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func MediaLibraryPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.MediaLibrary())
}

func ListCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	items, err := media.List(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as mídias")
		return
	}
	usage, err := repo.GetCompanyStorageUsage(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar o armazenamento")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"items": items, "usage": usage}})
}

func CleanupCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		Before string `json:"before"`
	}
	if err := ctx.ReadJSON(&body); err != nil || strings.TrimSpace(body.Before) == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "informe a data limite da limpeza")
		return
	}
	cutoff, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(body.Before), time.UTC)
	if err != nil || cutoff.After(time.Now().UTC()) {
		writeJSONError(ctx, iris.StatusBadRequest, "a data limite deve ser válida e não pode estar no futuro")
		return
	}
	items, err := media.List(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as mídias")
		return
	}
	var deletedCount, skippedCount int
	var deletedBytes int64
	for _, item := range items {
		if !item.CreatedAt.Before(cutoff) {
			continue
		}
		if item.Scope == media.ScopeFlows {
			references, refErr := repo.CountFlowMediaReferences(agent.CompanyId, item.Name)
			if refErr != nil {
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso das mídias")
				return
			}
			messageReferences, refErr := repo.CountMessageMediaReferences(agent.CompanyId, item.Scope, item.Name)
			if refErr != nil {
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso das mídias")
				return
			}
			if references > 0 || messageReferences > 0 {
				skippedCount++
				continue
			}
		}
		size, deleteErr := media.Delete(agent.CompanyId, item.Scope, item.Name)
		if errors.Is(deleteErr, os.ErrNotExist) {
			continue
		}
		if deleteErr != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível concluir a limpeza das mídias")
			return
		}
		if item.Scope == media.ScopeMessages {
			if clearErr := repo.ClearMessageMediaPath(agent.CompanyId, item.Name); clearErr != nil {
				writeJSONError(ctx, iris.StatusInternalServerError, "mídias excluídas, mas não foi possível atualizar as mensagens")
				return
			}
		}
		if storageErr := repo.DecrementCompanyStorage(agent.CompanyId, size); storageErr != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "mídias excluídas, mas não foi possível atualizar o consumo")
			return
		}
		deletedCount++
		deletedBytes += size
	}
	ctx.JSON(iris.Map{"data": iris.Map{"deletedCount": deletedCount, "skippedCount": skippedCount, "deletedBytes": deletedBytes}})
}

func DownloadCompanyMedia(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	path, err := media.ScopedPath(agent.CompanyId, ctx.Params().Get("scope"), ctx.Params().Get("file"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": ctx.Params().Get("file")}))
	if err := ctx.ServeFile(path); err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
	}
}

func PreviewCompanyMedia(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	item, err := media.Inspect(agent.CompanyId, ctx.Params().Get("scope"), ctx.Params().Get("file"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	path, _ := media.ScopedPath(agent.CompanyId, item.Scope, item.Name)
	ctx.Header("X-Content-Type-Options", "nosniff")
	// The authenticated workspace embeds this response in its own document reader.
	// The global DENY policy would prevent that same-origin iframe from rendering.
	ctx.ResponseWriter().Header().Set("X-Frame-Options", "SAMEORIGIN")
	ctx.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": item.Name}))
	if item.MimeType != "" {
		ctx.ContentType(item.MimeType)
	}
	if err := ctx.ServeFile(path); err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
	}
}

func ServeForwardMedia(ctx iris.Context) {
	companyID, err := ctx.Params().GetInt("companyId")
	if err != nil || companyID <= 0 {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	expires, err := strconv.ParseInt(ctx.URLParam("expires"), 10, 64)
	if err != nil || !media.VerifyScoped(companyID, ctx.Params().Get("scope"), ctx.Params().Get("file"), expires, ctx.URLParam("token")) {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	item, err := media.Inspect(companyID, ctx.Params().Get("scope"), ctx.Params().Get("file"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	path, _ := media.ScopedPath(companyID, item.Scope, item.Name)
	ctx.Header("Cache-Control", "private, max-age=300")
	ctx.Header("X-Content-Type-Options", "nosniff")
	if item.MimeType != "" {
		ctx.ContentType(item.MimeType)
	}
	if err := ctx.ServeFile(path); err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
	}
}

func ForwardCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	allowed, err := repo.CompanyHasAccess(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o acesso da empresa")
		return
	}
	if !allowed {
		writeJSONError(ctx, iris.StatusPaymentRequired, "a operação da empresa está indisponível; verifique o plano e o armazenamento")
		return
	}
	var body struct {
		ContactId    int    `json:"contactId"`
		ConnectionId int    `json:"connectionId"`
		Caption      string `json:"caption"`
	}
	if err := ctx.ReadJSON(&body); err != nil || body.ContactId <= 0 || body.ConnectionId <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "contato e conexão são obrigatórios")
		return
	}
	body.Caption = strings.TrimSpace(body.Caption)
	if len(body.Caption) > 1000 {
		writeJSONError(ctx, iris.StatusBadRequest, "a legenda deve ter até 1000 caracteres")
		return
	}
	item, err := media.Inspect(agent.CompanyId, ctx.Params().Get("scope"), ctx.Params().Get("file"))
	if err != nil {
		writeJSONError(ctx, iris.StatusNotFound, "mídia não encontrada")
		return
	}
	if item.Kind != "image" && item.Kind != "video" && item.Kind != "audio" && item.Kind != "document" {
		writeJSONError(ctx, iris.StatusBadRequest, "tipo de mídia não suportado")
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar a política de anexos")
		return
	}
	allowedKind := false
	for _, kind := range settings.AllowedAttachmentTypes {
		if kind == item.Kind {
			allowedKind = true
			break
		}
	}
	if !allowedKind || item.SizeBytes > int64(settings.MaxAttachmentMB)<<20 {
		writeJSONError(ctx, iris.StatusForbidden, "esta mídia não atende à política de anexos da empresa")
		return
	}
	started, err := repo.StartOrReuseConversation(agent, body.ContactId, body.ConnectionId)
	if err != nil {
		if errors.Is(err, repo.ErrConversationUnavailable) {
			writeJSONError(ctx, iris.StatusConflict, "o contato está indisponível nesta conexão")
		} else if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "contato ou conexão não encontrado")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível abrir a conversa")
		}
		return
	}
	conversation, err := repo.GetVisibleConversationByURL(started.URL, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar a conversa")
		return
	}
	connection, err := repo.GetConnectionById(conversation.ConnectionID)
	if err != nil || connection.CompanyID != agent.CompanyId || connection.Status != repo.ConnectionStatusConnected {
		writeJSONError(ctx, iris.StatusConflict, "a conexão selecionada não está disponível")
		return
	}
	mediaURL, err := media.ScopedSignedURL(agent.CompanyId, item.Scope, item.Name, 15*time.Minute)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível preparar a mídia para envio")
		return
	}
	messageID, err := flowengine.NewChatMessenger(connection, settings).SendStoredMediaMessage(conversation, item.Kind, mediaURL, body.Caption, item.Name, item.Scope, item.Name)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadGateway, "não foi possível enviar a mídia")
		return
	}
	if agent.IsSupport {
		recordCompanyAction(ctx, agent, "send_media", "message", messageID)
	}
	sse.NotifyConversations(agent.CompanyId)
	ctx.JSON(iris.Map{"data": iris.Map{"conversationURL": started.URL}})
}

func DeleteCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	scope := ctx.Params().Get("scope")
	fileName := ctx.Params().Get("file")
	if scope == media.ScopeFlows {
		references, err := repo.CountFlowMediaReferences(agent.CompanyId, fileName)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso da mídia")
			return
		}
		if references > 0 {
			writeJSONError(ctx, iris.StatusConflict, "esta mídia está sendo usada por uma automação ou execução")
			return
		}
		messageReferences, err := repo.CountMessageMediaReferences(agent.CompanyId, scope, fileName)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso da mídia")
			return
		}
		if messageReferences > 0 {
			writeJSONError(ctx, iris.StatusConflict, "esta mídia já foi enviada em uma conversa")
			return
		}
	}
	size, err := media.Delete(agent.CompanyId, scope, fileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(ctx, iris.StatusNotFound, "mídia não encontrada")
			return
		}
		writeJSONError(ctx, iris.StatusBadRequest, "não foi possível excluir a mídia")
		return
	}
	if scope == media.ScopeMessages {
		if err := repo.ClearMessageMediaPath(agent.CompanyId, fileName); err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "mídia excluída, mas não foi possível atualizar as mensagens")
			return
		}
	}
	if err := repo.DecrementCompanyStorage(agent.CompanyId, size); err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "mídia excluída, mas não foi possível atualizar o consumo")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}
