package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Chat endpoints of the OpenAI-compatible providers, variables so tests can
// point them at a local server (groqChatURL lives in media.go).
var (
	openAIChatURL   = "https://api.openai.com/v1/chat/completions"
	deepseekChatURL = "https://api.deepseek.com/chat/completions"
)

// MaxToolSteps bounds the request/tool round trips of one answer. The last
// step forbids new tool calls so the model must write its reply.
const MaxToolSteps = 6

// ToolDefinition is one function the model may call. Parameters is a JSON
// Schema object ({"type":"object","properties":{...},"required":[...]}).
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// ToolExecutor runs a call and returns the text the model receives as its
// result. Failures are reported in that text so the model can explain them.
type ToolExecutor func(ctx context.Context, call ToolCall) string

// CompleteWithTools answers like Complete, letting the model call tools
// until it writes a final text reply. Token usage adds up every step.
func CompleteWithTools(ctx context.Context, request Request, tools []ToolDefinition, execute ToolExecutor) (Response, error) {
	if len(tools) == 0 || execute == nil {
		return Complete(ctx, request)
	}
	request = prepareRequest(request)
	client := &http.Client{Timeout: 75 * time.Second}
	switch request.Provider {
	case "openai":
		return completeOpenAITools(ctx, client, openAIChatURL, request, tools, execute)
	case "deepseek":
		return completeOpenAITools(ctx, client, deepseekChatURL, request, tools, execute)
	case "groq":
		return completeOpenAITools(ctx, client, groqChatURL, request, tools, execute)
	case "gemini":
		return completeGeminiTools(ctx, client, request, tools, execute)
	case "claude":
		return completeClaudeTools(ctx, client, request, tools, execute)
	default:
		return Response{}, errors.New("provedor de IA inválido")
	}
}

func toolSchema(tool ToolDefinition) map[string]interface{} {
	if tool.Parameters != nil {
		return tool.Parameters
	}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func toolArguments(raw string) map[string]interface{} {
	arguments := map[string]interface{}{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &arguments)
	}
	return arguments
}

func completeOpenAITools(ctx context.Context, client *http.Client, endpoint string, request Request, tools []ToolDefinition, execute ToolExecutor) (Response, error) {
	messages := make([]map[string]interface{}, 0, len(request.Messages)+4)
	if request.System != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": request.System})
	}
	for _, message := range request.Messages {
		messages = append(messages, map[string]interface{}{"role": message.Role, "content": message.Content})
	}
	specs := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, map[string]interface{}{"type": "function", "function": map[string]interface{}{
			"name": tool.Name, "description": tool.Description, "parameters": toolSchema(tool),
		}})
	}
	var total Response
	for step := 0; step < MaxToolSteps; step++ {
		payload := map[string]interface{}{"model": request.Model, "messages": messages, "max_tokens": request.MaxOutputTokens,
			"temperature": request.Temperature, "tools": specs}
		if step == MaxToolSteps-1 {
			payload["tool_choice"] = "none"
		}
		var result struct {
			Choices []struct {
				Message struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
					ToolCalls        []struct {
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := doJSON(ctx, client, request.Provider, endpoint, request.APIKey, "", payload, &result); err != nil {
			return Response{}, err
		}
		total.PromptTokens += result.Usage.PromptTokens
		total.CompletionTokens += result.Usage.CompletionTokens
		if len(result.Choices) == 0 {
			return Response{}, errors.New("provedor retornou uma resposta vazia")
		}
		message := result.Choices[0].Message
		content := ""
		if message.Content != nil {
			content = strings.TrimSpace(*message.Content)
		}
		if len(message.ToolCalls) == 0 {
			if content == "" {
				return Response{}, errors.New("provedor retornou uma resposta vazia")
			}
			total.Text = content
			return total, nil
		}
		assistant := map[string]interface{}{"role": "assistant", "content": message.Content, "tool_calls": message.ToolCalls}
		// DeepSeek thinking models require their reasoning back within the turn.
		if message.ReasoningContent != nil {
			assistant["reasoning_content"] = *message.ReasoningContent
		}
		messages = append(messages, assistant)
		for _, call := range message.ToolCalls {
			output := execute(ctx, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: toolArguments(call.Function.Arguments)})
			messages = append(messages, map[string]interface{}{"role": "tool", "tool_call_id": call.ID, "content": output})
		}
	}
	return Response{}, errors.New("o modelo não concluiu a resposta após usar as ações")
}

func completeClaudeTools(ctx context.Context, client *http.Client, request Request, tools []ToolDefinition, execute ToolExecutor) (Response, error) {
	messages := make([]interface{}, 0, len(request.Messages)+4)
	for _, message := range request.Messages {
		messages = append(messages, map[string]interface{}{"role": message.Role, "content": message.Content})
	}
	specs := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, map[string]interface{}{"name": tool.Name, "description": tool.Description, "input_schema": toolSchema(tool)})
	}
	var total Response
	for step := 0; step < MaxToolSteps; step++ {
		payload := claudePayload(request.Model, request.MaxOutputTokens, request.Temperature, request.System, messages)
		payload["tools"] = specs
		if step == MaxToolSteps-1 {
			payload["tool_choice"] = map[string]string{"type": "none"}
		}
		var result struct {
			Content    []json.RawMessage `json:"content"`
			StopReason string            `json:"stop_reason"`
			Usage      struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := doJSON(ctx, client, "claude", claudeURL, request.APIKey, "2023-06-01", payload, &result); err != nil {
			return Response{}, err
		}
		total.PromptTokens += result.Usage.InputTokens
		total.CompletionTokens += result.Usage.OutputTokens
		var texts []string
		var results []map[string]interface{}
		for _, raw := range result.Content {
			var block struct {
				Type  string                 `json:"type"`
				Text  string                 `json:"text"`
				ID    string                 `json:"id"`
				Name  string                 `json:"name"`
				Input map[string]interface{} `json:"input"`
			}
			if json.Unmarshal(raw, &block) != nil {
				continue
			}
			switch block.Type {
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					texts = append(texts, text)
				}
			case "tool_use":
				if block.Input == nil {
					block.Input = map[string]interface{}{}
				}
				output := execute(ctx, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Input})
				results = append(results, map[string]interface{}{"type": "tool_result", "tool_use_id": block.ID, "content": output})
			}
		}
		if len(results) == 0 {
			if len(texts) == 0 {
				if err := claudeEmptyResponseError(result.StopReason); err != nil {
					return Response{}, err
				}
				return Response{}, errors.New("Claude retornou uma resposta vazia")
			}
			total.Text = strings.Join(texts, "\n")
			return total, nil
		}
		// Blocks go back unchanged: thinking blocks carry signatures Claude checks.
		messages = append(messages, map[string]interface{}{"role": "assistant", "content": result.Content},
			map[string]interface{}{"role": "user", "content": results})
	}
	return Response{}, errors.New("o modelo não concluiu a resposta após usar as ações")
}

func completeGeminiTools(ctx context.Context, client *http.Client, request Request, tools []ToolDefinition, execute ToolExecutor) (Response, error) {
	contents := make([]interface{}, 0, len(request.Messages)+4)
	for _, message := range request.Messages {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]interface{}{"role": role, "parts": []map[string]string{{"text": message.Content}}})
	}
	declarations := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		declaration := map[string]interface{}{"name": tool.Name, "description": tool.Description}
		// Gemini rejects an object schema without properties.
		if properties, _ := toolSchema(tool)["properties"].(map[string]interface{}); len(properties) > 0 {
			declaration["parameters"] = toolSchema(tool)
		}
		declarations = append(declarations, declaration)
	}
	endpoint := geminiAPIBase + "/models/" + url.PathEscape(request.Model) + ":generateContent"
	var total Response
	for step := 0; step < MaxToolSteps; step++ {
		payload := map[string]interface{}{
			"contents":          contents,
			"systemInstruction": map[string]interface{}{"parts": []map[string]string{{"text": request.System}}},
			"generationConfig":  map[string]interface{}{"maxOutputTokens": request.MaxOutputTokens, "temperature": request.Temperature},
			"tools":             []interface{}{map[string]interface{}{"functionDeclarations": declarations}},
		}
		if step == MaxToolSteps-1 {
			payload["toolConfig"] = map[string]interface{}{"functionCallingConfig": map[string]string{"mode": "NONE"}}
		}
		var result struct {
			Candidates []struct {
				Content struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			Usage struct {
				PromptTokens    int `json:"promptTokenCount"`
				CandidateTokens int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := doJSON(ctx, client, "gemini", endpoint, request.APIKey, "", payload, &result); err != nil {
			return Response{}, err
		}
		total.PromptTokens += result.Usage.PromptTokens
		total.CompletionTokens += result.Usage.CandidateTokens
		if len(result.Candidates) == 0 {
			return Response{}, errors.New("Gemini retornou uma resposta vazia")
		}
		parts := result.Candidates[0].Content.Parts
		var texts []string
		var responses []map[string]interface{}
		for _, raw := range parts {
			var part struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					ID   string                 `json:"id"`
					Name string                 `json:"name"`
					Args map[string]interface{} `json:"args"`
				} `json:"functionCall"`
			}
			if json.Unmarshal(raw, &part) != nil {
				continue
			}
			if part.FunctionCall != nil {
				if part.FunctionCall.Args == nil {
					part.FunctionCall.Args = map[string]interface{}{}
				}
				output := execute(ctx, ToolCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args})
				response := map[string]interface{}{"name": part.FunctionCall.Name, "response": map[string]string{"result": output}}
				if part.FunctionCall.ID != "" {
					response["id"] = part.FunctionCall.ID
				}
				responses = append(responses, map[string]interface{}{"functionResponse": response})
			} else if text := strings.TrimSpace(part.Text); text != "" && !part.Thought {
				texts = append(texts, text)
			}
		}
		if len(responses) == 0 {
			if len(texts) == 0 {
				return Response{}, errors.New("Gemini retornou uma resposta vazia")
			}
			total.Text = strings.Join(texts, "\n")
			return total, nil
		}
		// Parts go back unchanged: Gemini 3 requires their thought signatures.
		contents = append(contents, map[string]interface{}{"role": "model", "parts": parts},
			map[string]interface{}{"role": "user", "parts": responses})
	}
	return Response{}, errors.New("o modelo não concluiu a resposta após usar as ações")
}
