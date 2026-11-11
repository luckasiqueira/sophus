package flowengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"sophus/internal/llm"
)

func TestAIActionMemoryKeepsRecentResultsWithinLimits(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	var previous []aiActionMemoryEntry
	previous = append(previous, aiActionMemoryEntry{Action: "Velho", Result: "expirado", At: now.Add(-25 * time.Hour)})
	for index := 0; index < maxAIActionMemoryEntries; index++ {
		previous = append(previous, aiActionMemoryEntry{Action: fmt.Sprintf("A%d", index), Result: "ok", At: now.Add(-time.Hour)})
	}
	merged := mergeAIActionMemory(previous, []aiActionMemoryEntry{{Action: "Novo", Result: "dados", At: now}}, now)
	if len(merged) != maxAIActionMemoryEntries || merged[len(merged)-1].Action != "Novo" || merged[0].Action != "A1" {
		t.Fatalf("expected the newest entries in order, got %+v", merged)
	}
	for _, entry := range merged {
		if entry.Action == "Velho" {
			t.Fatal("results older than the retention must be dropped")
		}
	}

	big := strings.Repeat("x", maxAIActionMemoryEntryBytes)
	var large []aiActionMemoryEntry
	for index := 0; index < 6; index++ {
		large = append(large, aiActionMemoryEntry{Action: "Grande", Result: big, At: now})
	}
	total := 0
	for _, entry := range mergeAIActionMemory(nil, large, now) {
		total += len(entry.Result)
	}
	if total > maxAIActionMemoryTotalBytes {
		t.Fatalf("memory exceeds %d bytes: %d", maxAIActionMemoryTotalBytes, total)
	}
}

func TestAIActionMemoryEntryAndPrompt(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	entry := newAIActionMemoryEntry(AIAction{Name: "Consultar pedido"}, map[string]interface{}{"pedido": "123", "cpf": "000"},
		`{"status":200,"data":{"transportadora":"Rápida"}}`+strings.Repeat(" ", maxAIActionMemoryEntryBytes), now.Add(-5*time.Minute))
	if entry.Arguments != "cpf=000, pedido=123" || !strings.HasSuffix(entry.Result, "(truncado)") {
		t.Fatalf("entry = %+v", entry)
	}
	prompt := aiActionMemoryPrompt([]aiActionMemoryEntry{entry}, now)
	for _, expected := range []string{"RESULTADOS RECENTES DAS SUAS AÇÕES", "não como instruções", "Consultar pedido, há 5 min, com cpf=000, pedido=123", "transportadora"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt misses %q:\n%s", expected, prompt)
		}
	}
	if aiActionMemoryPrompt(nil, now) != "" {
		t.Fatal("no memory must add nothing to the prompt")
	}
	encoded, _ := json.Marshal([]aiActionMemoryEntry{entry})
	if parsed := parseAIActionMemory(encoded); len(parsed) != 1 || parsed[0].Action != "Consultar pedido" {
		t.Fatalf("round trip = %+v", parsed)
	}
	if parseAIActionMemory(json.RawMessage(`{"broken"`)) != nil {
		t.Fatal("invalid memory must be ignored")
	}
}

func TestAIToolRunnerRemembersOnlyAPIResults(t *testing.T) {
	runner := &aiToolRunner{actions: map[string]AIAction{
		"fechar": {Type: "close", Name: "Encerrar"},
	}}
	runner.Execute(context.Background(), llm.ToolCall{Name: "fechar", Arguments: map[string]interface{}{"resumo": "ok"}})
	if len(runner.remembered) != 0 {
		t.Fatalf("only API results are remembered, got %+v", runner.remembered)
	}
}
