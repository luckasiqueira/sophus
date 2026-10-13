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

func TestHumanOwnedConversationSkipsFlowDispatch(t *testing.T) {
	agentID := 42
	conversation := repo.Conversation{Status: repo.ConversationStatusOpen, AgentID: &agentID}
	if shouldDispatchIncomingAutomation(conversation) {
		t.Fatal("human-owned open conversation must not dispatch automation")
	}
	handleIncomingMessage(conversation, repo.ConnectionEVO{}, "olá", "Cliente", "text", true)
	conversation.Status = repo.ConversationStatusPending
	if shouldDispatchIncomingAutomation(conversation) {
		t.Fatal("human-owned pending conversation must not dispatch automation")
	}
	conversation.AgentID = nil
	if !shouldDispatchIncomingAutomation(conversation) {
		t.Fatal("unassigned conversation should remain eligible for automation")
	}
}
