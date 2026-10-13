package web

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"sophus/internal/repo"
)

func TestInternalNoteAppearsChronologicallyAndEscapesText(t *testing.T) {
	now := time.Now().UTC()
	notes := []repo.ConversationEvent{{ID: 1, EventType: "note", Content: "<script>private</script>", Metadata: json.RawMessage(`{"authorName":"Agente Ana"}`), CreatedAt: now.Add(time.Minute)}}
	messages := []repo.MessageData{{MessageId: "first", Text: "Mensagem anterior", CreatedAt: now}, {MessageId: "last", Text: "Mensagem posterior", CreatedAt: now.Add(2 * time.Minute)}}
	items := conversationTimeline(messages, notes)
	if len(items) != 3 || items[1].Note == nil || items[1].Note.ID != 1 {
		t.Fatalf("note not ordered between messages: %+v", items)
	}
	var html bytes.Buffer
	if err := MessageSingle(repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusClosed}, repo.Agent{Id: 7}, nil, messages, notes, repo.ConversationScopeCounts{}).Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Nota interna", "Agente Ana", "Não enviada ao WhatsApp", `data-internal-note-id="1"`, "/notes", "internal-note"} {
		if !strings.Contains(html.String(), expected) {
			t.Fatalf("missing %q", expected)
		}
	}
	if strings.Contains(html.String(), "<script>private</script>") {
		t.Fatal("note rendered unescaped HTML")
	}
	if !strings.Contains(html.String(), `style="background-color: #fef3c7; border-color: #fcd34d;"`) {
		t.Fatal("note balloon has no distinct color")
	}
	if !strings.Contains(html.String(), "DOMContentLoaded', initializeConversationNotes") {
		t.Fatal("note initialization must wait until composer exists")
	}
}
