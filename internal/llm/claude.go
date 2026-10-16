package llm

import "strings"

// claudeSamplingFreeModels reject sampling parameters: Claude Haiku 5.5,
// Sonnet 5.x, Opus 5.x, Opus 4.7/4.8 and Fable answer 400 to a non-default
// temperature, so it is only sent to the older families that accept it.
var claudeSamplingFreeModels = []string{"claude-haiku-5", "claude-sonnet-5", "claude-opus-5", "claude-opus-4-7", "claude-opus-4-8", "claude-fable-"}

func claudeAcceptsTemperature(model string) bool {
	for _, prefix := range claudeSamplingFreeModels {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}
	return true
}

// claudeEffort keeps Claude Haiku 5.5 close to Haiku 4.5 in latency and cost:
// it always thinks adaptively (default effort medium) and thinking tokens
// count against max_tokens, so chat replies and media summaries run at low.
func claudeEffort(model string) string {
	if strings.HasPrefix(model, "claude-haiku-5") {
		return "low"
	}
	return ""
}

func claudePayload(model string, maxTokens int, temperature float64, system string, messages interface{}) map[string]interface{} {
	payload := map[string]interface{}{"model": model, "max_tokens": maxTokens, "messages": messages}
	if system != "" {
		payload["system"] = system
	}
	if claudeAcceptsTemperature(model) {
		payload["temperature"] = temperature
	}
	if effort := claudeEffort(model); effort != "" {
		payload["output_config"] = map[string]string{"effort": effort}
	}
	return payload
}

// claudeEmptyResponseError explains a response without text: a refusal
// (Haiku 5.5 has no server-side fallback model) or output cut by max_tokens.
func claudeEmptyResponseError(stopReason string) error {
	switch stopReason {
	case "refusal":
		return &providerError{ProviderFailure: ProviderFailure{Provider: "claude", Category: "refusal"}}
	case "max_tokens":
		return &providerError{ProviderFailure: ProviderFailure{Provider: "claude", Category: "max_tokens"}}
	}
	return nil
}
