package controllers

import (
	"strings"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func AgentPreferencesPage(ctx iris.Context) {
	if _, err := middlewares.AgentIdentifier(ctx); err != nil {
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.RenderComponent(web.AgentPreferencesPage())
}

func GetAgentPreferences(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	preferences, err := requestAgentPreferences(ctx, agent)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar suas preferências")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"data": preferences, "isAdmin": agent.IsAdmin()})
}

func SaveAgentOnboarding(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	if agent.IsSupport {
		ctx.StatusCode(iris.StatusNoContent)
		return
	}
	var state repo.AgentOnboarding
	if err := ctx.ReadJSON(&state); err != nil || state.Step < 0 || state.Step > 30 {
		writeJSONError(ctx, iris.StatusBadRequest, "progresso do guia inválido")
		return
	}
	if err := repo.SaveAgentOnboarding(agent.Id, agent.CompanyId, state); err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar o progresso do guia")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func UpdateAgentPreferences(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	preferences := repo.DefaultAgentPreferences()
	if err := ctx.ReadJSON(&preferences); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	preferences.MessageSignatureTemplate = strings.TrimSpace(preferences.MessageSignatureTemplate)
	if preferences.ScrollAfterSendMode != "always" && preferences.ScrollAfterSendMode != "except_quotes" {
		writeJSONError(ctx, iris.StatusBadRequest, "comportamento da rolagem inválido")
		return
	}
	if utf8.RuneCountInString(preferences.MessageSignatureTemplate) > 160 {
		writeJSONError(ctx, iris.StatusBadRequest, "a assinatura deve ter até 160 caracteres")
		return
	}
	if preferences.MessageSignatureTemplate == "" {
		preferences.MessageSignatureTemplate = "{agentName}"
	}
	if session, support := middlewares.SupportSession(ctx); support {
		if err := repo.UpdatePlatformSupportPreferences(session, preferences); err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar as preferências da sessão de suporte")
			return
		}
		preferences.Onboarding.Completed = true
		ctx.JSON(iris.Map{"data": preferences})
		return
	}
	if err := repo.UpdateAgentPreferences(agent.Id, agent.CompanyId, preferences); err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar suas preferências")
		return
	}
	preferences, err = repo.GetAgentPreferences(agent.Id, agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "preferências salvas; recarregue a página para conferir")
		return
	}
	ctx.JSON(iris.Map{"data": preferences})
}

func requestAgentPreferences(ctx iris.Context, agent repo.Agent) (repo.AgentPreferences, error) {
	if session, support := middlewares.SupportSession(ctx); support {
		return session.Preferences, nil
	}
	return repo.GetAgentPreferences(agent.Id, agent.CompanyId)
}
