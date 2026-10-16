package llm

type ModelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var providerModels = map[string][]ModelOption{
	"openai": {
		{ID: "gpt-4o-mini", Name: "GPT-4o mini"},
		{ID: "gpt-4o", Name: "GPT-4o"},
		{ID: "gpt-4.1-mini", Name: "GPT-4.1 mini"},
		{ID: "gpt-4.1", Name: "GPT-4.1"},
	},
	"deepseek": {
		{ID: "deepseek-flash", Name: "DeepSeek Flash"},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
	},
	"groq": {
		{ID: "openai/gpt-oss-20b", Name: "GPT-OSS 20B"},
		{ID: "openai/gpt-oss-120b", Name: "GPT-OSS 120B"},
		{ID: groqVisionModel, Name: "Llama 4 Scout (lê imagens)"},
	},
	// Google limits the 2.5 models to projects that already used them; new keys
	// get 404 and must pick a current model. 2.5 stays for existing credentials.
	"gemini": {
		{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash (recomendado)"},
		{ID: "gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash-Lite"},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash (só projetos que já usavam)"},
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro (só projetos que já usavam)"},
	},
	"claude": {
		{ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5 (recomendado)"},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
		{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
		{ID: "claude-opus-5", Name: "Claude Opus 5"},
	},
}

func ProviderModels() map[string][]ModelOption {
	result := make(map[string][]ModelOption, len(providerModels))
	for provider, models := range providerModels {
		result[provider] = append([]ModelOption(nil), models...)
	}
	return result
}

func IsSupportedModel(provider, model string) bool {
	for _, option := range providerModels[provider] {
		if option.ID == model {
			return true
		}
	}
	return false
}
