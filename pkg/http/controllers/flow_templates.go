package controllers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

type flowTemplateRequest struct {
	Name     string `json:"name"`
	Content  string `json:"content"`
	Revision int    `json:"revision"`
}

func ListFlowTemplates(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	templates, err := repo.ListFlowMessageTemplates(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.JSON(iris.Map{"data": templates})
}

func CreateFlowTemplate(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	request, ok := readFlowTemplateRequest(ctx, false)
	if !ok {
		return
	}
	template, err := repo.CreateFlowMessageTemplate(repo.FlowMessageTemplate{
		CompanyID: agent.CompanyId, Name: request.Name, Content: request.Content,
	})
	if err != nil {
		if isUniqueViolation(err) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "já existe um modelo com esse nome"})
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": template})
}

func UpdateFlowTemplate(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	request, ok := readFlowTemplateRequest(ctx, true)
	if !ok {
		return
	}
	template := repo.FlowMessageTemplate{
		ID: id, CompanyID: agent.CompanyId, Name: request.Name, Content: request.Content, Revision: request.Revision,
	}
	if err := repo.UpdateFlowMessageTemplate(&template); err != nil {
		if errors.Is(err, repo.ErrFlowTemplateConflict) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "o modelo foi alterado ou removido; recarregue a biblioteca"})
			return
		}
		if isUniqueViolation(err) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "já existe um modelo com esse nome"})
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.JSON(iris.Map{"data": template})
}

func DeleteFlowTemplate(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	referenced, err := repo.ActiveFlowReferencesTemplate(id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if referenced {
		ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "não é possível excluir um modelo usado por um fluxo ativo"})
		return
	}
	if err := repo.DeleteFlowMessageTemplate(id, agent.CompanyId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func readFlowTemplateRequest(ctx iris.Context, requireRevision bool) (flowTemplateRequest, bool) {
	var request flowTemplateRequest
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&request); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return request, false
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Content = strings.TrimSpace(request.Content)
	if request.Name == "" || len(request.Name) > 100 || request.Content == "" || len(request.Content) > 10000 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome e conteúdo do modelo são obrigatórios"})
		return request, false
	}
	if requireRevision && request.Revision <= 0 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "revision é obrigatória"})
		return request, false
	}
	return request, true
}
