package controllers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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
	Priority     int             `json:"priority"`
}

type updateFlowRequest struct {
	Name         *string                `json:"name"`
	Description  *string                `json:"description"`
	ConnectionId nullableFlowConnection `json:"connectionId"`
	TriggerType  *string                `json:"triggerType"`
	TriggerValue *string                `json:"triggerValue"`
	FlowData     *json.RawMessage       `json:"flowData"`
	IsActive     *bool                  `json:"isActive"`
	Priority     *int                   `json:"priority"`
	Revision     *int                   `json:"revision"`
}

type nullableFlowConnection struct {
	Set   bool
	Value *int
}

func (value *nullableFlowConnection) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var id int
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	value.Value = &id
	return nil
}

func ParseFlowCURL(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "JSON inválido"})
		return
	}
	config, err := flowengine.ParseCURL(body.Command)
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": config})
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
	if req.Priority < -1000 || req.Priority > 1000 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "prioridade deve estar entre -1000 e 1000"})
		return
	}
	if req.TriggerType == "" {
		req.TriggerType = "keyword"
	}
	req.TriggerType = strings.ToLower(strings.TrimSpace(req.TriggerType))
	req.TriggerValue = strings.TrimSpace(req.TriggerValue)
	if !flowConnectionBelongsToCompany(req.ConnectionId, agent.CompanyId) {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "conexão inválida para esta empresa"})
		return
	}
	if err := validateRequestedFlow(req.FlowData, req.IsActive, req.TriggerType, req.TriggerValue); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.ValidateFlowDependencies(req.FlowData, agent.CompanyId, 0); err != nil {
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
		Priority:     req.Priority,
		CreatedBy:    &createdBy,
	}
	id, err := repo.CreateChatbotFlow(flow)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	flow.Id = id
	flow.Revision = 1
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
	if req.Revision == nil || *req.Revision <= 0 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "revision é obrigatória para atualizar o fluxo"})
		return
	}
	if req.Name != nil {
		flow.Name = *req.Name
	}
	if req.Description != nil {
		flow.Description = *req.Description
	}
	if req.ConnectionId.Set {
		if !flowConnectionBelongsToCompany(req.ConnectionId.Value, agent.CompanyId) {
			ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "conexão inválida para esta empresa"})
			return
		}
		flow.ConnectionId = req.ConnectionId.Value
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
	if req.Priority != nil {
		flow.Priority = *req.Priority
	}
	if req.Revision != nil {
		flow.Revision = *req.Revision
	}
	flow.TriggerType = strings.ToLower(strings.TrimSpace(flow.TriggerType))
	flow.TriggerValue = strings.TrimSpace(flow.TriggerValue)
	if flow.Priority < -1000 || flow.Priority > 1000 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "prioridade deve estar entre -1000 e 1000"})
		return
	}
	if err := validateRequestedFlow(flow.FlowData, flow.IsActive, flow.TriggerType, flow.TriggerValue); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.ValidateFlowDependencies(flow.FlowData, agent.CompanyId, flow.Id); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.UpdateChatbotFlow(&flow); err != nil {
		if errors.Is(err, repo.ErrFlowRevisionConflict) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "este fluxo foi alterado em outra sessão; recarregue antes de salvar"})
			return
		}
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
		if errors.Is(err, repo.ErrFlowHasActiveExecutions) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "não é possível excluir um fluxo com execuções ativas"})
			return
		}
		if errors.Is(err, repo.ErrFlowHasExecutions) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "não é possível excluir um fluxo que possui histórico de execuções"})
			return
		}
		if errors.Is(err, repo.ErrFlowReferenced) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "não é possível excluir um fluxo usado como subfluxo ativo"})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func validateRequestedFlow(data json.RawMessage, active bool, triggerType, triggerValue string) error {
	triggerType = strings.ToLower(strings.TrimSpace(triggerType))
	if triggerType != "keyword" && triggerType != "exact" && triggerType != "always" {
		return errors.New("tipo de gatilho inválido")
	}
	if active && triggerType != "always" && strings.TrimSpace(triggerValue) == "" {
		return errors.New("gatilho ativo exige um valor")
	}
	if active {
		return flowengine.ValidateActiveFlowData(data)
	}
	return flowengine.ValidateFlowData(data)
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
