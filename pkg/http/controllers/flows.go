package controllers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/web"
	"strings"

	"github.com/kataras/iris/v12"
)

const maxFlowAPIRequestBytes = 2 << 20

func DownloadFlowKnowledgeTemplate(ctx iris.Context) {
	content := "BT\n/F1 18 Tf\n72 740 Td\n(MODELO DE BASE DE CONHECIMENTO) Tj\n/F1 11 Tf\n0 -32 Td\n(Preencha cada secao com informacoes curtas, atualizadas e verificaveis.) Tj\n0 -28 Td\n(# EMPRESA) Tj\n0 -16 Td\n(Nome: Sua empresa) Tj\n0 -16 Td\n(Descricao: O que voce faz e para quem.) Tj\n0 -28 Td\n(# PRODUTOS E SERVICOS) Tj\n0 -16 Td\n(- Nome: descricao, publico indicado e beneficios.) Tj\n0 -16 Td\n(- Preco: valor, condicoes e formas de pagamento.) Tj\n0 -28 Td\n(# POLITICAS) Tj\n0 -16 Td\n(- Trocas, cancelamentos, prazos, entrega e suporte.) Tj\n0 -28 Td\n(# PERGUNTAS FREQUENTES) Tj\n0 -16 Td\n(Pergunta: Como funciona?) Tj\n0 -16 Td\n(Resposta: Explique em poucas frases e inclua condicoes importantes.) Tj\n0 -28 Td\n(# ESCALONAMENTO) Tj\n0 -16 Td\n(Quando a IA deve transferir para uma pessoa: casos financeiros, urgentes ou fora destas regras.) Tj\nET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
	}
	pdf := "%PDF-1.4\n"
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = len(pdf)
		pdf += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := len(pdf)
	pdf += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		pdf += fmt.Sprintf("%010d 00000 n \n", offset)
	}
	pdf += fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	ctx.Header("Content-Disposition", `attachment; filename="modelo-base-conhecimento.pdf"`)
	ctx.ContentType("application/pdf")
	_, _ = ctx.WriteString(pdf)
}

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

func TestFlowHTTPRequest(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	var body struct {
		Config  map[string]interface{} `json:"config"`
		Context map[string]interface{} `json:"context"`
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxFlowAPIRequestBytes)
	if err := ctx.ReadJSON(&body); err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "JSON inválido"})
		return
	}
	for key := range body.Context {
		if strings.HasPrefix(key, "_") {
			ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "contexto contém chave reservada"})
			return
		}
	}
	response, err := flowengine.TestHTTPRequest(body.Config, flowengine.ExecutionContext(body.Context))
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"data": response})
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

func ListFlowAIAgents(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	agents, err := repo.ListAIAgents(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível listar os agentes de IA"})
		return
	}
	result := make([]iris.Map, 0, len(agents))
	for _, aiAgent := range agents {
		if aiAgent.IsActive {
			result = append(result, iris.Map{"id": aiAgent.Id, "name": aiAgent.Name, "credentialId": aiAgent.CredentialId, "provider": aiAgent.Provider, "model": aiAgent.Model, "safeMode": aiAgent.SafeMode})
		}
	}
	ctx.JSON(iris.Map{"data": result})
}

func ListFlowAICredentials(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	credentials, err := repo.ListAICredentials(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível listar as credenciais de IA"})
		return
	}
	result := make([]iris.Map, 0, len(credentials))
	for _, credential := range credentials {
		result = append(result, iris.Map{
			"id": credential.Id, "name": credential.Name, "provider": credential.Provider, "model": credential.Model,
		})
	}
	ctx.JSON(iris.Map{"data": result})
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
