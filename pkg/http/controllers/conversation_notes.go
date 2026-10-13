package controllers

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/web/components"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func AddConversationNote(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "dados da nota inválidos")
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || utf8.RuneCountInString(input.Content) > 10000 {
		writeJSONError(ctx, iris.StatusBadRequest, "escreva uma nota de até 10.000 caracteres")
		return
	}
	if _, err := repo.GetVisibleConversationByURL(conversationURL, agent); err != nil {
		writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar as configurações")
		return
	}
	event, err := repo.AddAgentConversationNote(conversationURL.String(), agent, input.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusForbidden, "você não tem acesso a esta conversa")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar a nota")
		}
		return
	}
	html, err := renderComponent(components.InternalNote(event, settings.Timezone))
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "nota salva, mas não foi possível exibi-la")
		return
	}
	sse.Global.SendEvent(conversationURL.String(), "internal-note", html)
	ctx.StatusCode(iris.StatusCreated)
	ctx.ContentType("text/html")
	ctx.WriteString(html)
}
