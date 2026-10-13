package llm

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"sophus/utils/env"
)

const maxResponseBytes = 2 << 20

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Provider        string
	Model           string
	APIKey          string
	System          string
	Messages        []Message
	MaxOutputTokens int
	Temperature     float64
	SafeMode        bool
}

type Response struct {
	Text             string
	PromptTokens     int
	CompletionTokens int
}

type ProviderFailure struct {
	Provider   string
	StatusCode int
	Category   string
	Retryable  bool
}

type providerError struct {
	ProviderFailure
	cause error
}

func (err *providerError) Error() string {
	switch err.Category {
	case "authentication":
		return "a chave de API foi recusada pelo provedor"
	case "invalid_request":
		return "o provedor recusou a solicitação; verifique o modelo selecionado"
	case "rate_limit":
		return "o limite de uso do provedor foi atingido"
	case "unavailable":
		return "o provedor de IA está indisponível no momento"
	default:
		return "não foi possível conectar ao provedor de IA"
	}
}

func (err *providerError) Unwrap() error { return err.cause }

func FailureForError(err error) (ProviderFailure, bool) {
	var providerErr *providerError
	if errors.As(err, &providerErr) {
		return providerErr.ProviderFailure, true
	}
	return ProviderFailure{}, false
}

func SafeErrorMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "o provedor de IA não respondeu a tempo"
	}
	failure, ok := FailureForError(err)
	if !ok {
		return "não foi possível concluir a solicitação ao provedor de IA"
	}
	switch failure.Category {
	case "authentication":
		return "a chave de API foi recusada pelo provedor"
	case "invalid_request":
		return "o provedor recusou a solicitação; verifique o modelo configurado"
	case "model_not_found":
		return "o modelo não está disponível para esta credencial"
	case "credit_required":
		return "a conta do provedor não possui saldo disponível"
	case "rate_limit":
		return "o limite de uso do provedor foi atingido"
	default:
		return "o provedor de IA está indisponível no momento"
	}
}

// AnalyzeMedia sends a stored attachment to the provider and returns only its
// transcription or a concise textual description.
func AnalyzeMedia(ctx context.Context, provider, model, apiKey, kind, mimeType string, data []byte, prompt string, safeMode bool) (Response, error) {
	if !SupportsMediaAnalysis(provider, kind) {
		if provider == "groq" {
			return Response{}, errors.New("Groq suporta somente transcrição de áudio")
		}
		return Response{}, errors.New("provedor incompatível com transcrição de mídia")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	if kind == "audio" {
		endpoint, transcriptionModel := audioTranscriptionConfig(provider, model)
		if safeMode {
			prompt = SafeTranscriptionPrompt(prompt)
		}
		return transcribeAudio(ctx, client, provider, endpoint, transcriptionModel, apiKey, mimeType, data, prompt)
	}
	instruction := strings.TrimSpace(prompt)
	if instruction == "" {
		instruction = "Transcreva ou resuma este arquivo. Retorne somente o texto útil, sem comentários sobre o processo."
	}
	encoded := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	var input map[string]interface{}
	if kind == "image" {
		input = map[string]interface{}{"type": "input_image", "image_url": encoded}
	} else {
		input = map[string]interface{}{"type": "input_file", "filename": "attachment", "file_data": encoded}
	}
	payload := map[string]interface{}{
		"model": model,
		"input": []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "input_text", "text": instruction}, input,
		}}},
	}
	if safeMode {
		payload["instructions"] = SafeMediaInstructions()
	}
	var result struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := doJSON(ctx, client, "openai", "https://api.openai.com/v1/responses", apiKey, "", payload, &result); err != nil {
		return Response{}, err
	}
	for _, item := range result.Output {
		for _, part := range item.Content {
			if strings.TrimSpace(part.Text) != "" {
				return Response{Text: strings.TrimSpace(part.Text)}, nil
			}
		}
	}
	return Response{}, errors.New("OpenAI retornou uma resposta vazia")
}

func SupportsMediaAnalysis(provider, kind string) bool {
	return provider == "openai" || (provider == "groq" && kind == "audio")
}

func audioTranscriptionConfig(provider, configuredModel string) (endpoint, model string) {
	if provider == "groq" {
		return "https://api.groq.com/openai/v1/audio/transcriptions", "whisper-large-v3-turbo"
	}
	model = configuredModel
	if !strings.Contains(strings.ToLower(model), "transcri") && !strings.Contains(strings.ToLower(model), "whisper") {
		model = "whisper-1"
	}
	return "https://api.openai.com/v1/audio/transcriptions", model
}

func transcribeAudio(ctx context.Context, client *http.Client, provider, endpoint, model, apiKey, mimeType string, data []byte, prompt string) (Response, error) {
	var result struct {
		Text string `json:"text"`
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", model); err != nil {
		return Response{}, err
	}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		if err := writer.WriteField("prompt", prompt); err != nil {
			return Response{}, err
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio"`)
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return Response{}, err
	}
	if _, err := part.Write(data); err != nil {
		return Response{}, err
	}
	if err := writer.Close(); err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, &providerError{ProviderFailure: ProviderFailure{Provider: provider, Category: "network", Retryable: true}, cause: err}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, limited)
		return Response{}, providerErrorForStatus(provider, resp.StatusCode)
	}
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(result.Text) == "" {
		return Response{}, errors.New("o provedor retornou uma transcrição vazia")
	}
	return Response{Text: strings.TrimSpace(result.Text)}, nil
}

func EncryptAPIKey(value string) (string, error) {
	key, err := credentialsKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(strings.TrimSpace(value)), []byte("sophus-ai-key-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func DecryptAPIKey(value string) (string, error) {
	key, err := credentialsKey()
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("credencial de IA inválida")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return "", errors.New("credencial de IA inválida")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("sophus-ai-key-v1"))
	if err != nil {
		return "", errors.New("não foi possível decifrar a credencial de IA")
	}
	return string(plain), nil
}

func credentialsKey() ([]byte, error) {
	secret := strings.TrimSpace(env.Backend["AI_CREDENTIALS_KEY"])
	if secret == "" {
		secret = strings.TrimSpace(env.Backend["SALT_TOKEN"])
	}
	if secret == "" {
		return nil, errors.New("AI_CREDENTIALS_KEY não configurada")
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

func Complete(ctx context.Context, request Request) (Response, error) {
	request = prepareRequest(request)
	client := &http.Client{Timeout: 75 * time.Second}
	switch request.Provider {
	case "openai":
		return completeOpenAICompatible(ctx, client, "https://api.openai.com/v1/chat/completions", request)
	case "deepseek":
		return completeOpenAICompatible(ctx, client, "https://api.deepseek.com/chat/completions", request)
	case "groq":
		return completeOpenAICompatible(ctx, client, "https://api.groq.com/openai/v1/chat/completions", request)
	case "gemini":
		return completeGemini(ctx, client, request)
	case "claude":
		return completeClaude(ctx, client, request)
	default:
		return Response{}, errors.New("provedor de IA inválido")
	}
}

func prepareRequest(request Request) Request {
	if request.SafeMode {
		request.System = ApplySafeMode(request.System)
	}
	return request
}

func completeOpenAICompatible(ctx context.Context, client *http.Client, endpoint string, request Request) (Response, error) {
	messages := make([]Message, 0, len(request.Messages)+1)
	if request.System != "" {
		messages = append(messages, Message{Role: "system", Content: request.System})
	}
	messages = append(messages, request.Messages...)
	payload := map[string]interface{}{"model": request.Model, "messages": messages, "max_tokens": request.MaxOutputTokens, "temperature": request.Temperature}
	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := doJSON(ctx, client, request.Provider, endpoint, request.APIKey, "", payload, &result); err != nil {
		return Response{}, err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return Response{}, errors.New("provedor retornou uma resposta vazia")
	}
	return Response{Text: strings.TrimSpace(result.Choices[0].Message.Content), PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens}, nil
}

func completeClaude(ctx context.Context, client *http.Client, request Request) (Response, error) {
	payload := map[string]interface{}{"model": request.Model, "system": request.System, "messages": request.Messages, "max_tokens": request.MaxOutputTokens, "temperature": request.Temperature}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := doJSON(ctx, client, "claude", "https://api.anthropic.com/v1/messages", request.APIKey, "2023-06-01", payload, &result); err != nil {
		return Response{}, err
	}
	var parts []string
	for _, content := range result.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			parts = append(parts, strings.TrimSpace(content.Text))
		}
	}
	if len(parts) == 0 {
		return Response{}, errors.New("Claude retornou uma resposta vazia")
	}
	return Response{Text: strings.Join(parts, "\n"), PromptTokens: result.Usage.InputTokens, CompletionTokens: result.Usage.OutputTokens}, nil
}

func completeGemini(ctx context.Context, client *http.Client, request Request) (Response, error) {
	contents := make([]map[string]interface{}, 0, len(request.Messages))
	for _, message := range request.Messages {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]interface{}{"role": role, "parts": []map[string]string{{"text": message.Content}}})
	}
	payload := map[string]interface{}{
		"contents":          contents,
		"systemInstruction": map[string]interface{}{"parts": []map[string]string{{"text": request.System}}},
		"generationConfig":  map[string]interface{}{"maxOutputTokens": request.MaxOutputTokens, "temperature": request.Temperature},
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage struct {
			PromptTokens    int `json:"promptTokenCount"`
			CandidateTokens int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(request.Model) + ":generateContent?key=" + url.QueryEscape(request.APIKey)
	if err := doJSON(ctx, client, "gemini", endpoint, "", "", payload, &result); err != nil {
		return Response{}, err
	}
	var parts []string
	if len(result.Candidates) > 0 {
		for _, part := range result.Candidates[0].Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, strings.TrimSpace(part.Text))
			}
		}
	}
	if len(parts) == 0 {
		return Response{}, errors.New("Gemini retornou uma resposta vazia")
	}
	return Response{Text: strings.Join(parts, "\n"), PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CandidateTokens}, nil
}

func doJSON(ctx context.Context, client *http.Client, provider, endpoint, bearer, anthropicVersion string, payload, target interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		if anthropicVersion != "" {
			req.Header.Set("x-api-key", bearer)
			req.Header.Set("anthropic-version", anthropicVersion)
		} else {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		return &providerError{ProviderFailure: ProviderFailure{Provider: provider, Category: "network", Retryable: true}, cause: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return errors.New("resposta do provedor excedeu o limite")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerErrorForStatus(provider, response.StatusCode)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("resposta inválida do provedor de IA: %w", err)
	}
	return nil
}

func providerErrorForStatus(provider string, statusCode int) *providerError {
	failure := ProviderFailure{Provider: provider, StatusCode: statusCode}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.Category = "authentication"
	case http.StatusPaymentRequired:
		failure.Category = "credit_required"
	case http.StatusNotFound:
		failure.Category = "model_not_found"
	case http.StatusTooManyRequests:
		failure.Category, failure.Retryable = "rate_limit", true
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		failure.Category = "invalid_request"
	default:
		failure.Category, failure.Retryable = "unavailable", statusCode >= http.StatusInternalServerError
	}
	return &providerError{ProviderFailure: failure}
}
