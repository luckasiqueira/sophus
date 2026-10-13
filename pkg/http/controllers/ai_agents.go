package controllers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sophus/internal/llm"
	"sophus/internal/repo"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"github.com/lib/pq"
)

type aiAgentRequest struct {
	ConnectionId    *int    `json:"connectionId"`
	Name            string  `json:"name"`
	CredentialId    int     `json:"credentialId"`
	SystemPrompt    string  `json:"systemPrompt"`
	KnowledgeBase   string  `json:"knowledgeBase"`
	Tone            string  `json:"tone"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
	Temperature     float64 `json:"temperature"`
	HistoryLimit    int     `json:"historyLimit"`
	WelcomeMessage  string  `json:"welcomeMessage"`
	SafeMode        *bool   `json:"safeMode"`
	IsActive        bool    `json:"isActive"`
	Revision        int     `json:"revision"`
}

func AIAgentSettingsPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.AIAgentSettings())
}

func ListAIAgents(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	agents, err := repo.ListAIAgents(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os agentes de IA")
		return
	}
	ctx.JSON(iris.Map{"data": agents})
}

func ListAIAgentConnections(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	connections, err := repo.GetConnectionListByCompany(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as conexões")
		return
	}
	result := make([]iris.Map, 0, len(connections))
	for _, connection := range connections {
		result = append(result, iris.Map{"id": connection.Id, "name": connection.Name, "number": connection.Number, "status": connection.Status})
	}
	ctx.JSON(iris.Map{"data": result})
}

func ListAIAgentModels(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.JSON(iris.Map{"data": llm.ProviderModels()})
}

func ExtractAIAgentKnowledgePDF(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	const maxPDFBytes = 10 << 20
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxPDFBytes+(1<<20))
	file, header, err := ctx.FormFile("file")
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "envie um arquivo PDF")
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > maxPDFBytes {
		writeJSONError(ctx, iris.StatusBadRequest, "o PDF deve possuir no máximo 10 MB")
		return
	}
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(file, prefix); err != nil || !bytes.Equal(prefix, []byte("%PDF-")) {
		writeJSONError(ctx, iris.StatusBadRequest, "o arquivo enviado não é um PDF válido")
		return
	}
	temporary, err := os.CreateTemp("", "sophus-ai-knowledge-*.pdf")
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível processar o PDF")
		return
	}
	path := temporary.Name()
	defer os.Remove(path)
	written, copyErr := io.Copy(temporary, io.MultiReader(bytes.NewReader(prefix), io.LimitReader(file, maxPDFBytes+1)))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil || written > maxPDFBytes {
		writeJSONError(ctx, iris.StatusBadRequest, "não foi possível receber o PDF")
		return
	}
	text, err := llm.ExtractPDFText(path)
	if err != nil {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, err.Error())
		return
	}
	name := filepath.Base(strings.TrimSpace(header.Filename))
	if name == "." || name == "" {
		name = "documento.pdf"
	}
	ctx.JSON(iris.Map{"data": iris.Map{"fileName": name, "text": text, "characters": len(text)}})
}

func CreateAIAgent(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body aiAgentRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	input, message := normalizeAIAgentRequest(&body)
	if message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	if _, err := repo.GetAICredential(input.CredentialId, current.CompanyId); err != nil {
		writeAIAgentError(ctx, err, "não foi possível validar a credencial de IA")
		return
	}
	agent, err := repo.CreateAIAgent(current.CompanyId, input)
	if err != nil {
		writeAIAgentError(ctx, err, "não foi possível criar o agente de IA")
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": agent})
}

func UpdateAIAgent(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body aiAgentRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if body.Revision <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "revisão inválida")
		return
	}
	input, message := normalizeAIAgentRequest(&body)
	if message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	if _, err := repo.GetAICredential(input.CredentialId, current.CompanyId); err != nil {
		writeAIAgentError(ctx, err, "não foi possível validar a credencial de IA")
		return
	}
	agent, err := repo.UpdateAIAgent(id, current.CompanyId, input)
	if err != nil {
		writeAIAgentError(ctx, err, "não foi possível atualizar o agente de IA")
		return
	}
	ctx.JSON(iris.Map{"data": agent})
}

func DeleteAIAgent(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	referenced, err := repo.ActiveFlowReferencesAIAgent(id, current.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso do agente")
		return
	}
	if referenced {
		writeJSONError(ctx, iris.StatusConflict, "o agente está vinculado a um flow ativo")
		return
	}
	deleted, err := repo.DeleteAIAgent(id, current.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível excluir o agente de IA")
		return
	}
	if !deleted {
		writeJSONError(ctx, iris.StatusNotFound, "agente de IA não encontrado")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func TestAIAgent(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	agent, err := repo.GetAIAgent(id, current.CompanyId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "agente de IA não encontrado")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar o agente")
		}
		return
	}
	apiKey, err := llm.DecryptAPIKey(agent.APIKeyCiphertext)
	if err != nil {
		writeJSONError(ctx, iris.StatusServiceUnavailable, err.Error())
		return
	}
	testContext, cancel := context.WithTimeout(ctx.Request().Context(), aiCredentialTestTimeout)
	defer cancel()
	response, err := llm.Complete(testContext, llm.Request{
		Provider: agent.Provider, Model: agent.Model, APIKey: apiKey, System: "Responda em português do Brasil de forma breve.",
		Messages:        []llm.Message{{Role: "user", Content: "Responda apenas: configuração validada"}},
		MaxOutputTokens: min(agent.MaxOutputTokens, 100), Temperature: 0, SafeMode: agent.SafeMode,
	})
	if err != nil {
		writeLLMTestError(ctx, "agent_test", current.CompanyId, agent.CredentialId, agent.Provider, agent.Model, err)
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"message": response.Text}})
}

func normalizeAIAgentRequest(body *aiAgentRequest) (repo.AIAgentInput, string) {
	body.Name = strings.TrimSpace(body.Name)
	body.SystemPrompt = strings.TrimSpace(body.SystemPrompt)
	body.KnowledgeBase = strings.TrimSpace(body.KnowledgeBase)
	body.Tone = strings.ToLower(strings.TrimSpace(body.Tone))
	body.WelcomeMessage = strings.TrimSpace(body.WelcomeMessage)
	if body.ConnectionId != nil && *body.ConnectionId <= 0 {
		body.ConnectionId = nil
	}
	if body.Name == "" || len(body.Name) > 100 {
		return repo.AIAgentInput{}, "informe um nome de até 100 caracteres"
	}
	if body.CredentialId <= 0 {
		return repo.AIAgentInput{}, "credencial de IA é obrigatória"
	}
	if len(body.SystemPrompt) > 20000 || len(body.KnowledgeBase) > 500000 || len(body.WelcomeMessage) > 4000 {
		return repo.AIAgentInput{}, "instruções, base de conhecimento ou saudação excedem o limite"
	}
	if body.Tone != "professional" && body.Tone != "friendly" && body.Tone != "concise" && body.Tone != "empathetic" && body.Tone != "sales" {
		return repo.AIAgentInput{}, "tom de voz inválido"
	}
	if body.MaxOutputTokens < 1 || body.MaxOutputTokens > 32768 || body.Temperature < 0 || body.Temperature > 2 || body.HistoryLimit < 2 || body.HistoryLimit > 100 {
		return repo.AIAgentInput{}, "limites de geração inválidos"
	}
	safeMode := true
	if body.SafeMode != nil {
		safeMode = *body.SafeMode
	}
	return repo.AIAgentInput{
		ConnectionId: body.ConnectionId, Name: body.Name, CredentialId: body.CredentialId,
		SystemPrompt: body.SystemPrompt, KnowledgeBase: body.KnowledgeBase,
		Tone: body.Tone, MaxOutputTokens: body.MaxOutputTokens, Temperature: body.Temperature,
		HistoryLimit: body.HistoryLimit, WelcomeMessage: body.WelcomeMessage, SafeMode: safeMode, IsActive: body.IsActive, Revision: body.Revision,
	}, ""
}

func writeAIAgentError(ctx iris.Context, err error, fallback string) {
	var pqError *pq.Error
	switch {
	case errors.Is(err, repo.ErrAIAgentConflict):
		writeJSONError(ctx, iris.StatusConflict, "o agente foi alterado em outra sessão; recarregue a página")
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, iris.StatusNotFound, "agente ou conexão não encontrado")
	case errors.As(err, &pqError) && pqError.Code == "23505":
		writeJSONError(ctx, iris.StatusConflict, "esta conexão já está vinculada a outro agente de IA")
	default:
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
}
