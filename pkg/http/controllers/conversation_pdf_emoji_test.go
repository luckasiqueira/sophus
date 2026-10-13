package controllers

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"
)

func TestPDFEmojiSequences(t *testing.T) {
	for _, test := range []struct{ text, key string }{
		{"👉", "1f449"}, {"👍🏽", "1f44d-1f3fd"}, {"🇧🇷", "1f1e7-1f1f7"},
		{"❤️", "2764"}, {"1️⃣", "31-20e3"}, {"👩‍⚕️", "1f469-200d-2695-fe0f"},
		{"↗️", "2197"}, {"↔️", "2194"}, {"👨‍👩‍👧‍👦", "1f468-200d-1f469-200d-1f467-200d-1f466"},
	} {
		tokens := conversationPDFTokens(test.text)
		if len(tokens) != 1 || tokens[0].emoji != test.key {
			t.Errorf("tokens(%q) = %+v, want one image %s", test.text, tokens, test.key)
		}
	}
	tokens := conversationPDFTokens("Olá👉mundo\nBom dia 😀")
	var original strings.Builder
	for _, token := range tokens {
		original.WriteString(token.text)
	}
	if original.String() != "Olá👉mundo\nBom dia 😀" {
		t.Fatal("tokenizer lost message text or newlines")
	}
}

func TestConversationPDFUsesRealEmojiArtwork(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_EMOJI_CDN") == "" {
		t.Skip("set ZUBLY_TEST_EMOJI_CDN to verify emoji artwork download")
	}
	data, err := buildConversationExportPDF(repo.ConversationExport{Timezone: "UTC", Contact: repo.Contact{Name: "João"}, Conversations: []repo.ExportedConversation{{Entries: []repo.ConversationExportEntry{
		{Kind: "message", Text: "👉 Clique aqui 👍🏽 🇧🇷 ❤️ ↗️ 👨‍👩‍👧‍👦", CreatedAt: time.Now()},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("/Subtype /Image")); got < 6 {
		t.Fatalf("expected 6 emoji artwork images, got %d", got)
	}
}
