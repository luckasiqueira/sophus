package controllers

import (
	"database/sql"
	"errors"
	"strings"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func DepartmentSettingsPage(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil || !agent.IsAdmin() {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	ctx.RenderComponent(web.DepartmentSettings())
}

func GetDepartmentSettings(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	departments, err := repo.GetDepartmentsByCompany(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	agents, err := repo.GetAgentAssignmentOptions(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"departments": departments, "agents": agents}})
}

func CreateDepartment(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := ctx.ReadJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome é obrigatório"})
		return
	}
	department, err := repo.CreateDepartment(agent.CompanyId, strings.TrimSpace(body.Name))
	if err != nil {
		ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": department})
}

func DeleteDepartment(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	deleted, err := repo.DeleteDepartment(id, agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if !deleted {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func UpdateAgentDepartments(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	agentID, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	var body struct {
		DepartmentIds []int `json:"departmentIds"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if err := repo.ReplaceAgentDepartments(agentID, agent.CompanyId, body.DepartmentIds); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func ListAssignmentOptions(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	departments, err := repo.GetDepartmentsByCompany(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	agents, err := repo.GetAgentAssignmentOptions(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"departments": departments, "agents": agents}})
}

func requireAdmin(ctx iris.Context) (repo.Agent, bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil || !agent.IsAdmin() {
		ctx.StopWithStatus(iris.StatusForbidden)
		return repo.Agent{}, false
	}
	return agent, true
}
