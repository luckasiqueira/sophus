package controllers

import (
	"encoding/json"
	"net/http"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/web"
	"strings"

	"github.com/kataras/iris/v12"
)

const maxFlowAPIRequestBytes = 2 << 20

type createFlowRequest struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConnectionId *int            `json:"connectionId"`
	TriggerType  string          `json:"triggerType"`
	TriggerValue string          `json:"triggerValue"`
	FlowData     json.RawMessage `json:"flowData"`
	IsActive     bool            `json:"isActive"`
}

type updateFlowRequest struct {
	Name         *string          `json:"name"`
	Description  *string          `json:"description"`
	ConnectionId *int             `json:"connectionId"`
	TriggerType  *string          `json:"triggerType"`
	TriggerValue *string          `json:"triggerValue"`
	FlowData     *json.RawMessage `json:"flowData"`
	IsActive     *bool            `json:"isActive"`
}

func ListFlows(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	flows, err := repo.GetChatbotFlowsByCompany(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": flows})
}

func GetFlow(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	flow, err := repo.GetChatbotFlowById(id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	ctx.JSON(iris.Map{"data": flow})
}

func CreateFlow(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var req createFlowRequest
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if req.Name == "" {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome é obrigatório"})
		return
	}
	if req.TriggerType == "" {
		req.TriggerType = "keyword"
	}
	if !flowConnectionBelongsToCompany(req.ConnectionId, agent.CompanyId) {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "conexão inválida para esta empresa"})
		return
	}
	if err := flowengine.ValidateFlowData(req.FlowData); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	createdBy := agent.Id
	flow := repo.ChatbotFlow{
		Name:         req.Name,
		Description:  req.Description,
		CompanyId:    agent.CompanyId,
		ConnectionId: req.ConnectionId,
		TriggerType:  req.TriggerType,
		TriggerValue: req.TriggerValue,
		FlowData:     req.FlowData,
		IsActive:     req.IsActive,
		CreatedBy:    &createdBy,
	}
	id, err := repo.CreateChatbotFlow(flow)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	flow.Id = id
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": flow})
}

func UpdateFlow(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	flow, err := repo.GetChatbotFlowById(id, agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	var req updateFlowRequest
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if req.Name != nil {
		flow.Name = *req.Name
	}
	if req.Description != nil {
		flow.Description = *req.Description
	}
	if req.ConnectionId != nil {
		if !flowConnectionBelongsToCompany(req.ConnectionId, agent.CompanyId) {
			ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "conexão inválida para esta empresa"})
			return
		}
		flow.ConnectionId = req.ConnectionId
	}
	if req.TriggerType != nil {
		flow.TriggerType = *req.TriggerType
	}
	if req.TriggerValue != nil {
		flow.TriggerValue = *req.TriggerValue
	}
	if req.FlowData != nil {
		if err := flowengine.ValidateFlowData(*req.FlowData); err != nil {
			ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
			return
		}
		flow.FlowData = *req.FlowData
	}
	if req.IsActive != nil {
		flow.IsActive = *req.IsActive
	}
	if err := repo.UpdateChatbotFlow(flow); err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": flow})
}

func DeleteFlow(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if err := repo.DeleteChatbotFlow(id, agent.CompanyId); err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func ExecuteFlowTest(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	var body struct {
		ConversationId int                    `json:"conversationId"`
		Context        map[string]interface{} `json:"context"`
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	for key := range body.Context {
		if strings.HasPrefix(key, "_") {
			ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "contexto contém chave reservada"})
			return
		}
	}
	conversation, err := repo.GetConversationById(body.ConversationId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	connection, err := repo.GetConnectionById(conversation.ConnectionID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if connection.CompanyID != agent.CompanyId {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	if conversation.Status != repo.ConversationStatusOpen {
		ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "a conversa precisa estar aberta para iniciar o flow"})
		return
	}
	engine := flowengine.NewEngine(connection)
	go func() {
		_ = engine.ExecuteFlow(id, body.ConversationId, agent.CompanyId, body.Context)
	}()
	ctx.JSON(iris.Map{"message": "flow enfileirado para execução"})
}

func flowConnectionBelongsToCompany(connectionID *int, companyID int) bool {
	if connectionID == nil {
		return true
	}
	connection, err := repo.GetConnectionById(*connectionID)
	return err == nil && connection.CompanyID == companyID
}

func FlowBuilderPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	ctx.Header("Pragma", "no-cache")
	ctx.Header("Expires", "0")
	ctx.RenderComponent(web.FlowBuilder())
}
