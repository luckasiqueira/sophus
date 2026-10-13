package flowengine

import (
	"strings"
	"testing"

	"sophus/internal/repo"
)

func TestNormalizeAIHistoryMergesConsecutiveRoles(t *testing.T) {
	messages := normalizeAIHistory([]repo.AIHistoryMessage{
		{Role: "assistant", Content: "old orphan"},
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "answer"},
	}, "current")
	if len(messages) != 3 {
		t.Fatalf("expected three alternating messages, got %#v", messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "first\nsecond" || messages[2].Content != "current" {
		t.Fatalf("unexpected normalized history: %#v", messages)
	}
}

func TestBuildAIAgentSystemPromptIncludesKnowledgeAndTone(t *testing.T) {
	prompt := buildAIAgentSystemPrompt(repo.AIAgent{Name: "Luna", Tone: "empathetic", SystemPrompt: "Não ofereça descontos.", KnowledgeBase: "Trocas em 30 dias."}, "troca")
	for _, expected := range []string{"Luna", "empatia", "Não ofereça descontos.", "Trocas em 30 dias."} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestRelevantKnowledgePrioritizesMatchingSections(t *testing.T) {
	knowledge := strings.Repeat("Informações gerais sem relação. ", 20) + "\n\n" + strings.Repeat("Política de troca em 30 dias. ", 20)
	selected := relevantKnowledge(knowledge, "Como faço uma troca?", 300)
	if !strings.Contains(selected, "Política de troca") {
		t.Fatalf("matching knowledge was not selected: %q", selected)
	}
}

func TestAIAgentInputDescribesMediaWithoutCaption(t *testing.T) {
	if got := aiAgentInput("", "audio"); !strings.Contains(got, "áudio") {
		t.Fatalf("unexpected media fallback: %q", got)
	}
}
