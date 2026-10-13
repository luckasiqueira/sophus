package controllers

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"golang.org/x/crypto/bcrypt"
)

func UserProfilePage(ctx iris.Context) {
	if _, err := middlewares.PlatformAdminIdentifier(ctx); err == nil {
		ctx.Redirect("/settings", iris.StatusFound)
		return
	}
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	if agent.IsSupport {
		ctx.Redirect("/preferences", iris.StatusFound)
		return
	}
	ctx.RenderComponent(web.UserProfile(agent))
}

func UpdateUserProfile(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	if agent.IsSupport {
		writeJSONError(ctx, iris.StatusForbidden, "o perfil de suporte é gerenciado pelo super admin")
		return
	}
	var body struct {
		Name            string `json:"name"`
		CurrentPassword string `json:"currentPassword"`
		Password        string `json:"password"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 40 {
		writeJSONError(ctx, iris.StatusBadRequest, "informe um nome de até 40 caracteres")
		return
	}
	passwordHash := ""
	if body.Password != "" {
		if len(body.Password) < 8 || len(body.Password) > 72 {
			writeJSONError(ctx, iris.StatusBadRequest, "a nova senha deve ter entre 8 e 72 bytes")
			return
		}
		credentials, err := repo.GetAgentByIdAndCompany(agent.Id, agent.CompanyId)
		if err != nil {
			writeJSONError(ctx, iris.StatusUnauthorized, "entre novamente para alterar a senha")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(credentials.Password), []byte(body.CurrentPassword)) != nil {
			writeJSONError(ctx, iris.StatusUnauthorized, "senha atual incorreta")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível proteger a senha")
			return
		}
		passwordHash = string(hash)
	}
	updated, err := repo.UpdateOwnAgentProfile(agent, body.Name, passwordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusConflict, "seu perfil mudou; entre novamente antes de salvar")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o perfil")
		}
		return
	}
	reauthenticate := updated.SessionVersion != agent.SessionVersion
	if reauthenticate {
		ctx.RemoveCookie("token", iris.CookiePath("/"))
	}
	recordCompanyAction(ctx, agent, "update_profile", "agent", "")
	ctx.JSON(iris.Map{"data": updated, "reauthenticate": reauthenticate})
}
