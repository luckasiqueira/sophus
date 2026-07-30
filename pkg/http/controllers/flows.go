package controllers

import (
	"encoding/json"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	var req createFlowRequest
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
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
		flow.ConnectionId = req.ConnectionId
	}
	if req.TriggerType != nil {
		flow.TriggerType = *req.TriggerType
	}
	if req.TriggerValue != nil {
		flow.TriggerValue = *req.TriggerValue
	}
	if req.FlowData != nil {
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
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
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
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
	engine := flowengine.NewEngine(connection)
	go func() {
		_ = engine.ExecuteFlow(id, body.ConversationId, agent.CompanyId, body.Context)
	}()
	ctx.JSON(iris.Map{"message": "flow enfileirado para execução"})
}

func FlowBuilderPage(ctx iris.Context) {
	ctx.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	ctx.Header("Pragma", "no-cache")
	ctx.Header("Expires", "0")
	ctx.ServeFile("./pkg/http/static/flowbuilder/index.html")
}
