package controllers

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

const defaultDepartmentColor = "#6123CC"

var departmentColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type departmentInput struct {
	Name  string `json:"name" form:"name"`
	Color string `json:"color" form:"color"`
}

type agentDepartmentsInput struct {
	DepartmentIds []int `json:"departmentIds" form:"departmentIds"`
}

func readDepartmentRequest(ctx iris.Context, target any) error {
	if strings.HasPrefix(strings.ToLower(ctx.GetHeader("Content-Type")), "application/json") {
		return ctx.ReadJSON(target)
	}
	return ctx.ReadForm(target)
}

func normalizeDepartmentInput(input departmentInput) (departmentInput, bool) {
	input.Name = strings.TrimSpace(input.Name)
	input.Color = strings.ToUpper(strings.TrimSpace(input.Color))
	if input.Color == "" {
		input.Color = defaultDepartmentColor
	}
	return input, input.Name != "" && len(input.Name) <= 80 && departmentColorPattern.MatchString(input.Color)
}

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
	var body departmentInput
	if err := readDepartmentRequest(ctx, &body); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "dados inválidos"})
		return
	}
	body, valid := normalizeDepartmentInput(body)
	if !valid {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome ou cor inválidos"})
		return
	}
	department, err := repo.CreateDepartment(agent.CompanyId, body.Name, body.Color)
	if err != nil {
		ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": department})
}

func UpdateDepartment(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	var body departmentInput
	if err := readDepartmentRequest(ctx, &body); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "dados inválidos"})
		return
	}
	body, valid := normalizeDepartmentInput(body)
	if !valid {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome ou cor inválidos"})
		return
	}
	department, err := repo.UpdateDepartment(id, agent.CompanyId, body.Name, body.Color)
	if errors.Is(err, sql.ErrNoRows) {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	if err != nil {
		ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": err.Error()})
		return
	}
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
	var body agentDepartmentsInput
	if err := readDepartmentRequest(ctx, &body); err != nil {
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
