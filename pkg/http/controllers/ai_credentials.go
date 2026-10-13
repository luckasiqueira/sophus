package controllers

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"sophus/internal/llm"
	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
	"github.com/lib/pq"
)

const aiCredentialTestTimeout = 15 * time.Second

type aiCredentialRequest struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	Revision int    `json:"revision"`
}

func ListAICredentials(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	credentials, err := repo.ListAICredentials(current.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as credenciais de IA")
		return
	}
	ctx.JSON(iris.Map{"data": credentials})
}

func ListAICredentialModels(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.JSON(iris.Map{"data": llm.ProviderModels()})
}

func CreateAICredential(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body aiCredentialRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	input, message := normalizeAICredentialRequest(&body, true)
	if message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	credential, err := repo.CreateAICredential(current.CompanyId, input)
	if err != nil {
		writeAICredentialError(ctx, err, "não foi possível criar a credencial de IA")
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": credential})
}

func UpdateAICredential(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body aiCredentialRequest
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if body.Revision <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "revisão inválida")
		return
	}
	input, message := normalizeAICredentialRequest(&body, false)
	if message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	credential, err := repo.UpdateAICredential(id, current.CompanyId, input)
	if err != nil {
		writeAICredentialError(ctx, err, "não foi possível atualizar a credencial de IA")
		return
	}
	ctx.JSON(iris.Map{"data": credential})
}

func DeleteAICredential(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	deleted, err := repo.DeleteAICredential(id, current.CompanyId)
	if err != nil {
		writeAICredentialError(ctx, err, "não foi possível excluir a credencial de IA")
		return
	}
	if !deleted {
		writeJSONError(ctx, iris.StatusNotFound, "credencial de IA não encontrada")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func TestAICredential(ctx iris.Context) {
	current, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	credential, err := repo.GetAICredential(id, current.CompanyId)
	if err != nil {
		writeAICredentialError(ctx, err, "não foi possível carregar a credencial de IA")
		return
	}
	apiKey, err := llm.DecryptAPIKey(credential.APIKeyCiphertext)
	if err != nil {
		writeJSONError(ctx, iris.StatusServiceUnavailable, err.Error())
		return
	}
	// Keep the provider probe below the reverse-proxy timeout so its failure is
	// returned to the browser as JSON instead of becoming an opaque 502.
	testContext, cancel := context.WithTimeout(ctx.Request().Context(), aiCredentialTestTimeout)
	defer cancel()
	response, err := llm.Complete(testContext, llm.Request{
		Provider: credential.Provider, Model: credential.Model, APIKey: apiKey,
		System:          "Responda em português do Brasil de forma breve.",
		Messages:        []llm.Message{{Role: "user", Content: "Responda apenas: configuração validada"}},
		MaxOutputTokens: 100,
	})
	if err != nil {
		writeLLMTestError(ctx, "credential_test", current.CompanyId, id, credential.Provider, credential.Model, err)
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"message": response.Text}})
}

func writeLLMTestError(ctx iris.Context, operation string, companyID, credentialID int, provider, model string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		log.Printf("llm request failed operation=%s company=%d credential=%d provider=%s model=%s category=timeout", operation, companyID, credentialID, provider, model)
		writeJSONError(ctx, iris.StatusGatewayTimeout, "o provedor de IA não respondeu ao teste em 15 segundos; verifique a disponibilidade do provedor")
		return
	}
	failure, known := llm.FailureForError(err)
	if !known {
		log.Printf("llm request failed operation=%s company=%d credential=%d provider=%s model=%s category=internal", operation, companyID, credentialID, provider, model)
		writeJSONError(ctx, iris.StatusBadGateway, "não foi possível testar a credencial de IA")
		return
	}
	log.Printf("llm request failed operation=%s company=%d credential=%d provider=%s model=%s category=%s upstream_status=%d retryable=%t", operation, companyID, credentialID, failure.Provider, model, failure.Category, failure.StatusCode, failure.Retryable)
	switch failure.Category {
	case "authentication", "invalid_request", "model_not_found":
		writeJSONError(ctx, iris.StatusUnprocessableEntity, failureErrorMessage(failure))
	case "credit_required":
		writeJSONError(ctx, iris.StatusPaymentRequired, failureErrorMessage(failure))
	case "rate_limit":
		writeJSONError(ctx, iris.StatusTooManyRequests, failureErrorMessage(failure))
	default:
		writeJSONError(ctx, iris.StatusServiceUnavailable, failureErrorMessage(failure))
	}
}

func failureErrorMessage(failure llm.ProviderFailure) string {
	switch failure.Category {
	case "authentication":
		return "a chave de API foi recusada pelo provedor; confirme a credencial cadastrada"
	case "invalid_request":
		return "o provedor recusou a solicitação; confirme se o modelo está disponível para esta chave"
	case "model_not_found":
		return "o modelo não está disponível para esta chave; edite a credencial e selecione um modelo atual"
	case "credit_required":
		return "a conta do provedor não possui saldo disponível; adicione créditos e tente novamente"
	case "rate_limit":
		return "o limite de uso do provedor foi atingido; tente novamente mais tarde"
	default:
		return "o provedor de IA está indisponível no momento; tente novamente mais tarde"
	}
}

func normalizeAICredentialRequest(body *aiCredentialRequest, creating bool) (repo.AICredentialInput, string) {
	body.Name = strings.TrimSpace(body.Name)
	body.Provider = strings.ToLower(strings.TrimSpace(body.Provider))
	body.Model = strings.TrimSpace(body.Model)
	body.APIKey = strings.TrimSpace(body.APIKey)
	if body.Name == "" || len(body.Name) > 100 {
		return repo.AICredentialInput{}, "informe um nome de até 100 caracteres"
	}
	if !llm.IsSupportedModel(body.Provider, body.Model) {
		return repo.AICredentialInput{}, "modelo não permitido para o provedor selecionado"
	}
	if creating && body.APIKey == "" {
		return repo.AICredentialInput{}, "informe a chave de API do provedor"
	}
	ciphertext := ""
	if body.APIKey != "" {
		var err error
		ciphertext, err = llm.EncryptAPIKey(body.APIKey)
		if err != nil {
			return repo.AICredentialInput{}, err.Error()
		}
	}
	return repo.AICredentialInput{Name: body.Name, Provider: body.Provider, Model: body.Model,
		APIKeyCiphertext: ciphertext, Revision: body.Revision}, ""
}

func writeAICredentialError(ctx iris.Context, err error, fallback string) {
	var pqError *pq.Error
	switch {
	case errors.Is(err, repo.ErrAICredentialConflict):
		writeJSONError(ctx, iris.StatusConflict, "a credencial foi alterada em outra sessão; recarregue a página")
	case errors.Is(err, repo.ErrAICredentialInUse):
		writeJSONError(ctx, iris.StatusConflict, "a credencial está vinculada a um agente de IA ou flow ativo")
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, iris.StatusNotFound, "credencial de IA não encontrada")
	case errors.As(err, &pqError) && pqError.Code == "23505":
		writeJSONError(ctx, iris.StatusConflict, "já existe uma credencial com este nome")
	case errors.As(err, &pqError) && pqError.Code == "23503":
		writeJSONError(ctx, iris.StatusConflict, "a credencial está em uso")
	default:
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
}
