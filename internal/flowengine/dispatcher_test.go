package flowengine

import (
	"testing"

	"sophus/internal/repo"
)

func TestMatchesTriggerAlwaysAcceptsEmptyText(t *testing.T) {
	flow := repo.ChatbotFlow{TriggerType: "always"}
	if !matchesTrigger(flow, "") {
		t.Fatal("always trigger should accept messages without text")
	}
}

func TestMatchesTriggerNormalizesType(t *testing.T) {
	flow := repo.ChatbotFlow{TriggerType: " Always "}
	if !matchesTrigger(flow, "olá") {
		t.Fatal("trigger type should be case-insensitive and trimmed")
	}
}

func TestMatchesTriggerExactRequiresValue(t *testing.T) {
	flow := repo.ChatbotFlow{TriggerType: "exact"}
	if matchesTrigger(flow, "") {
		t.Fatal("exact trigger should not match an empty value")
	}
}
