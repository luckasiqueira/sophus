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
		{name: "flow running", conversation: repo.Conversation{Status: repo.ConversationStatusRunning, AgentID: &agentID}, want: "pending"},
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

func TestQuotedMessageParticipant(t *testing.T) {
	tests := []struct {
		name       string
		message    repo.MessageData
		contact    repo.Contact
		connection repo.ConnectionEVO
		want       string
	}{
		{name: "incoming individual", contact: repo.Contact{JID: "5511999999999@s.whatsapp.net"}, want: "5511999999999@s.whatsapp.net"},
		{name: "incoming group", message: repo.MessageData{IsGroup: true, Sender: "5511888888888@s.whatsapp.net"}, want: "5511888888888@s.whatsapp.net"},
		{name: "outgoing", message: repo.MessageData{IsFromMe: true}, connection: repo.ConnectionEVO{Number: "5511777777777"}, want: "5511777777777@s.whatsapp.net"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := quotedMessageParticipant(test.message, test.contact, test.connection); got != test.want {
				t.Fatalf("quotedMessageParticipant() = %q, want %q", got, test.want)
			}
		})
	}
}
