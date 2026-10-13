package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
)

func parseConversationProtocol(value string) (int64, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "#"))
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	protocol, err := strconv.ParseInt(value, 10, 64)
	return protocol, err == nil && protocol > 0
}

func OpenConversationProtocol(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	protocol, valid := parseConversationProtocol(ctx.URLParam("protocol"))
	if !valid {
		writeJSONError(ctx, iris.StatusBadRequest, "informe um protocolo numérico válido, por exemplo #1002")
		return
	}
	conversation, err := repo.GetVisibleConversationByProtocol(protocol, agent)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "protocolo não encontrado ou conversa indisponível para seu usuário")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível pesquisar o protocolo")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	path := "/messages/" + conversation.URL.String()
	if strings.EqualFold(ctx.GetHeader("HX-Request"), "true") {
		location, _ := json.Marshal(iris.Map{"path": path, "target": "#conversation-panel", "swap": "outerHTML"})
		ctx.Header("HX-Location", string(location))
		ctx.StatusCode(iris.StatusNoContent)
		return
	}
	ctx.Redirect(path, iris.StatusFound)
}
