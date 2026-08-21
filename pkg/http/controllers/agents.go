package controllers

import (
	"database/sql"
	"errors"
	"strings"

	"sophus/internal/repo"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"golang.org/x/crypto/bcrypt"
)

type companyAgentRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	IsActive bool   `json:"isActive"`
}

func AgentSettingsPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.AgentSettings())
}

func ListCompanyAgents(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	agents, err := repo.ListAgentsByCompany(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os agentes")
		return
	}
	ctx.JSON(iris.Map{"data": agents})
}

func CreateCompanyAgent(ctx iris.Context) {
	currentAgent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body companyAgentRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if message := normalizeAndValidateAgent(&body, true); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível proteger a senha")
		return
	}
	agent, err := repo.CreateAgent(currentAgent.CompanyId, repo.CreateAgentInput{
		Name:         body.Name,
		Email:        body.Email,
		PasswordHash: string(passwordHash),
		Role:         body.Role,
		IsActive:     body.IsActive,
	})
	if err != nil {
		writeAgentRepositoryError(ctx, err, "não foi possível criar o agente")
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": agent})
}

func UpdateCompanyAgent(ctx iris.Context) {
	currentAgent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body companyAgentRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if message := normalizeAndValidateAgent(&body, false); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	passwordHash := ""
	if body.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível proteger a senha")
			return
		}
		passwordHash = string(hash)
	}
	updated, err := repo.UpdateAgent(id, currentAgent.CompanyId, repo.UpdateAgentInput{
		Name:         body.Name,
		Email:        body.Email,
		PasswordHash: passwordHash,
		Role:         body.Role,
		IsActive:     body.IsActive,
	})
	if err != nil {
		writeAgentRepositoryError(ctx, err, "não foi possível atualizar o agente")
		return
	}
	if !updated {
		writeJSONError(ctx, iris.StatusNotFound, "agente não encontrado")
		return
	}
	agent, err := repo.GetAgentByIdAndCompany(id, currentAgent.CompanyId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "agente não encontrado")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "agente atualizado, mas não foi possível recarregá-lo")
		return
	}
	ctx.JSON(iris.Map{"data": agent})
}

func normalizeAndValidateAgent(body *companyAgentRequest, passwordRequired bool) string {
	body.Name = strings.TrimSpace(body.Name)
	body.Email = normalizeEmail(body.Email)
	body.Role = strings.ToLower(strings.TrimSpace(body.Role))
	if body.Name == "" {
		return "nome é obrigatório"
	}
	if len(body.Name) > 40 {
		return "o nome deve ter até 40 caracteres"
	}
	if !validEmail(body.Email) {
		return "email inválido"
	}
	if body.Role != repo.RoleAdmin && body.Role != repo.RoleAgent {
		return "papel deve ser admin ou agent"
	}
	if passwordRequired && body.Password == "" {
		return "senha é obrigatória"
	}
	if body.Password != "" && len(body.Password) < 8 {
		return "a senha deve ter pelo menos 8 caracteres"
	}
	return ""
}

func writeAgentRepositoryError(ctx iris.Context, err error, fallback string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, iris.StatusNotFound, "agente não encontrado")
	case errors.Is(err, repo.ErrAgentEmailInUse), isUniqueViolation(err):
		writeJSONError(ctx, iris.StatusConflict, "este email já está em uso por outro agente")
	case errors.Is(err, repo.ErrInvalidAgentRole):
		writeJSONError(ctx, iris.StatusBadRequest, "papel deve ser admin ou agent")
	case errors.Is(err, repo.ErrLastActiveCompanyAdmin):
		writeJSONError(ctx, iris.StatusConflict, "não é possível remover o último administrador ativo")
	case errors.Is(err, repo.ErrAgentLimitReached):
		writeJSONError(ctx, iris.StatusConflict, "o limite de agentes do plano foi atingido")
	default:
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
}
