package controllers

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

type quickReplyInput struct {
	Shortcut string `json:"shortcut"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Revision int    `json:"revision"`
}

var quickReplyShortcutPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func normalizeQuickReplyInput(input quickReplyInput, update bool) (quickReplyInput, error) {
	input.Shortcut = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input.Shortcut), "/"))
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(input.Content, "\r\n", "\n"), "\r", "\n"))
	if !quickReplyShortcutPattern.MatchString(input.Shortcut) {
		return input, errors.New("use um atalho de até 40 caracteres, com letras sem acento, números, hífen ou sublinhado")
	}
	if !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) < 1 || utf8.RuneCountInString(input.Title) > 100 || strings.ContainsRune(input.Title, 0) {
		return input, errors.New("informe um título de até 100 caracteres")
	}
	if !utf8.ValidString(input.Content) || utf8.RuneCountInString(input.Content) < 1 || utf8.RuneCountInString(input.Content) > 10000 || strings.ContainsRune(input.Content, 0) {
		return input, errors.New("informe uma resposta de até 10000 caracteres")
	}
	if update && input.Revision <= 0 {
		return input, errors.New("recarregue a resposta antes de editar")
	}
	return input, nil
}

func QuickRepliesPage(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	ctx.RenderComponent(web.QuickReplies(agent.IsAdmin()))
}

func ListQuickReplies(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	replies, err := repo.ListQuickReplies(ctx.Request().Context(), agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar as respostas rápidas")
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(iris.Map{"data": replies})
}

func saveQuickReply(ctx iris.Context, update bool) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var input quickReplyInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "dados da resposta inválidos")
		return
	}
	input, err := normalizeQuickReplyInput(input, update)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, err.Error())
		return
	}
	reply := repo.QuickReply{CompanyID: agent.CompanyId, Shortcut: input.Shortcut, Title: input.Title, Content: input.Content, Revision: input.Revision}
	if update {
		reply.ID, err = ctx.Params().GetInt("id")
		if err != nil || reply.ID <= 0 {
			writeJSONError(ctx, iris.StatusBadRequest, "resposta inválida")
			return
		}
		err = repo.UpdateQuickReply(ctx.Request().Context(), &reply)
	} else {
		reply, err = repo.CreateQuickReply(ctx.Request().Context(), reply)
	}
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrQuickReplyConflict):
			writeJSONError(ctx, iris.StatusConflict, "a resposta foi alterada ou removida; recarregue a lista")
		case isUniqueViolation(err):
			writeJSONError(ctx, iris.StatusConflict, "já existe uma resposta com esse atalho")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar a resposta rápida")
		}
		return
	}
	if !update {
		ctx.StatusCode(iris.StatusCreated)
	}
	ctx.JSON(iris.Map{"data": reply})
}

func CreateQuickReply(ctx iris.Context) { saveQuickReply(ctx, false) }
func UpdateQuickReply(ctx iris.Context) { saveQuickReply(ctx, true) }

func DeleteQuickReply(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "resposta inválida")
		return
	}
	err = repo.DeleteQuickReply(ctx.Request().Context(), id, agent.CompanyId)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "resposta rápida não encontrada")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível excluir a resposta rápida")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}
