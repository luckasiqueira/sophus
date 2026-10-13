package web

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestConversationPanelIsStandaloneAndHistoryIsPaged(t *testing.T) {
	var html strings.Builder
	id := 7
	conversation := repo.Conversation{URL: uuid.New(), AgentID: &id, Status: "open"}
	if err := ConversationPanel(conversation, repo.Agent{Id: id}, nil, nil, repo.ConversationMessagePage{HasOlder: true, BeforeID: 100}).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="conversation-panel"`, `id="conversation-history-more"`, `data-before="100"`, "Carregar mensagens anteriores", "AbortController"} {
		if !strings.Contains(html.String(), expected) {
			t.Fatalf("partial panel missing %q", expected)
		}
	}
	if strings.Contains(html.String(), "<!doctype html>") || strings.Contains(html.String(), `id="app-navigation"`) {
		t.Fatal("partial panel rendered the entire shell")
	}
}

func TestConversationLinksUsePartialNavigationAndLazyAvatars(t *testing.T) {
	var html strings.Builder
	if err := ConversationSidebar([]repo.Conversation{{URL: uuid.New()}}, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`hx-target="#conversation-panel"`, `hx-push-url="true"`, `loading="lazy"`, `decoding="async"`, `hx-sync="#conversation-sidebar:replace"`} {
		if !strings.Contains(html.String(), expected) {
			t.Fatalf("sidebar missing %q", expected)
		}
	}
}
