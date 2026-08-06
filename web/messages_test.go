package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"sophus/internal/repo"

	"github.com/google/uuid"
)

func TestConversationPreview(t *testing.T) {
	tests := []struct {
		name         string
		conversation repo.Conversation
		want         string
	}{
		{name: "text", conversation: repo.Conversation{LastContactMessage: "  Olá  "}, want: "Olá"},
		{name: "image", conversation: repo.Conversation{LastContactMediaType: "image"}, want: "Imagem"},
		{name: "empty", conversation: repo.Conversation{}, want: "Nenhuma mensagem recebida"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationPreview(test.conversation); got != test.want {
				t.Fatalf("conversationPreview() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPendingRunningConversationShowsAcceptButton(t *testing.T) {
	conversation := repo.Conversation{
		Status: repo.ConversationStatusRunning,
		URL:    uuid.New(),
	}
	var output bytes.Buffer
	if err := ConversationList([]repo.Conversation{conversation}, "pending").Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation list: %v", err)
	}
	if !strings.Contains(output.String(), ">Aceitar</button>") {
		t.Fatalf("pending running conversation does not render accept button: %s", output.String())
	}
}
