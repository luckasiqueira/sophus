package llm

import "testing"

func TestSupportedModelsAreProviderScoped(t *testing.T) {
	if !IsSupportedModel("openai", "gpt-4o-mini") {
		t.Fatal("expected OpenAI model to be supported")
	}
	if !IsSupportedModel("groq", "openai/gpt-oss-20b") {
		t.Fatal("expected Groq model to be supported")
	}
	for _, model := range []string{"gemini-3.8-flash", "gemini-3.5-flash-lite", "gemini-2.5-flash"} {
		if !IsSupportedModel("gemini", model) {
			t.Fatalf("expected Gemini model %s to be supported", model)
		}
	}
	if ProviderModels()["gemini"][0].ID != "gemini-3.8-flash" {
		t.Fatal("new Gemini credentials should default to a model available to new keys")
	}
	if IsSupportedModel("gemini", "gpt-4o-mini") || IsSupportedModel("openai", "custom-model") {
		t.Fatal("model catalog accepted a model for the wrong provider or an arbitrary model")
	}
	models := ProviderModels()
	models["openai"][0].ID = "modified"
	if !IsSupportedModel("openai", "gpt-4o-mini") {
		t.Fatal("returned catalog mutated the internal catalog")
	}
}
