package controllers

import (
	"testing"

	"sophus/internal/repo"
)

func TestConversationTab(t *testing.T) {
	agentID := 1
	tests := []struct {
		name         string
		conversation repo.Conversation
		want         string
	}{
		{name: "unassigned", conversation: repo.Conversation{}, want: "pending"},
		{name: "assigned", conversation: repo.Conversation{AgentID: &agentID}, want: "active"},
		{name: "closed unassigned", conversation: repo.Conversation{Status: repo.ConversationStatusClosed}, want: "closed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationTab(test.conversation); got != test.want {
				t.Fatalf("conversationTab() = %q, want %q", got, test.want)
			}
		})
	}
}
