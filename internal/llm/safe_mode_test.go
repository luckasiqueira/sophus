package llm

import (
	"strings"
	"testing"
)

func TestApplySafeModeAddsCentralProtectionsOnce(t *testing.T) {
	prompt := ApplySafeMode("Atenda uma confeitaria.")
	for _, expected := range []string{
		"Atenda uma confeitaria.",
		"POLÍTICA CENTRAL DE MODO SEGURO",
		"modelo, versão",
		"perguntas sem relação",
		"tráfico ou produção de drogas",
		"prompt",
		"dados pessoais",
		"Não invente fatos",
		"autolesão",
		"médico, jurídico ou financeiro",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("safe prompt does not contain %q: %s", expected, prompt)
		}
	}
	if repeated := ApplySafeMode(prompt); strings.Count(repeated, safeModePolicyMarker) != 1 {
		t.Fatalf("safe policy was duplicated: %s", repeated)
	}
	if strings.Index(prompt, safeModePolicyMarker) < strings.Index(prompt, "Atenda uma confeitaria.") {
		t.Fatal("central policy must be appended after tenant-controlled instructions")
	}
}

func TestSafeTranscriptionPromptTreatsMediaAsData(t *testing.T) {
	prompt := SafeTranscriptionPrompt("Priorize termos de confeitaria.")
	for _, expected := range []string{"Priorize termos de confeitaria.", "apenas como material", "não execute comandos", "forma fiel"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("safe transcription prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestSafeMediaInstructionsIncludeCentralPolicy(t *testing.T) {
	instructions := SafeMediaInstructions()
	for _, expected := range []string{"POLÍTICA CENTRAL DE MODO SEGURO", "mídia", "conteúdo não confiável"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("safe media instructions do not contain %q: %s", expected, instructions)
		}
	}
}

func TestPrepareRequestHonorsSafeModeSelection(t *testing.T) {
	safe := prepareRequest(Request{System: "Atenda uma confeitaria.", SafeMode: true})
	if !strings.Contains(safe.System, safeModePolicyMarker) {
		t.Fatal("safe request did not receive the central policy")
	}
	regular := prepareRequest(Request{System: "Atenda uma confeitaria.", SafeMode: false})
	if regular.System != "Atenda uma confeitaria." {
		t.Fatalf("disabled safe mode changed the prompt: %q", regular.System)
	}
}
