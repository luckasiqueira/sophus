package repo

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func historyFixture(count int) []MessageData {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	messages := make([]MessageData, count)
	for index := range messages {
		messages[index] = MessageData{Id: index + 1, MessageId: time.Duration(index + 1).String(), Text: "Mensagem", CreatedAt: start.Add(time.Duration(index+1) * time.Second)}
	}
	return messages
}

func TestMessageContextIncludesFiveNeighborsAndHandlesEdges(t *testing.T) {
	messages := historyFixture(150)
	for _, test := range []struct{ anchor, first, last, count int }{
		{80, 75, 85, 11}, {1, 1, 6, 6}, {150, 145, 150, 6},
	} {
		preview, err := messageContext(messages, test.anchor)
		if err != nil {
			t.Fatal(err)
		}
		if len(preview) != test.count || preview[0].Id != test.first || preview[len(preview)-1].Id != test.last {
			t.Fatalf("context for %d has incorrect neighbors: %+v", test.anchor, preview)
		}
	}
	messages[78].IsDeleted = true
	messages[80].IsDeleted = true
	preview, err := messageContext(messages, 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 11 || preview[0].Id != 74 || preview[10].Id != 86 {
		t.Fatal("deleted neighbors reduced the visible context")
	}
	for _, message := range preview {
		if message.IsDeleted {
			t.Fatal("deleted content exposed in preview")
		}
	}
	for _, id := range []int{79, 999} {
		if _, err := messageContext(messages, id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unavailable target %d accepted: %v", id, err)
		}
	}
}

func TestCachedHistoryPagesHaveNoGapsOrOverlap(t *testing.T) {
	messages := historyFixture(150)
	page, err := historyPage(messages, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 50 || page.Messages[0].Id != 51 || page.Messages[49].Id != 100 || !page.HasOlder || page.BeforeID != 51 {
		t.Fatalf("incorrect older page: %+v", page)
	}
	previous, err := historyPage(messages, page.BeforeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Messages) != 50 || previous.Messages[0].Id != 1 || previous.Messages[49].Id != 50 || previous.HasOlder {
		t.Fatalf("incorrect final page: %+v", previous)
	}
}

func TestIncrementalHistoryKeepsFormerRecentWindowAfterBurst(t *testing.T) {
	original := historyFixture(150)
	updates := historyFixture(250)[150:]
	merged := mergeHistoryMessages(original, updates)
	page, err := historyPage(merged, 201)
	if err != nil {
		t.Fatal(err)
	}
	if page.Messages[0].Id != 151 || page.Messages[49].Id != 200 {
		t.Fatal("burst lost newly archived messages")
	}
	older, err := historyPage(merged, page.BeforeID)
	if err != nil {
		t.Fatal(err)
	}
	if older.Messages[0].Id != 101 || older.Messages[49].Id != 150 {
		t.Fatal("former recent 50-message buffer was lost")
	}
}

func TestHistoryUpsertsReceiptsAndSortsLateArrivals(t *testing.T) {
	original := historyFixture(100)
	changed := original[20]
	changed.DeliveryStatus = 3
	late := MessageData{Id: 101, MessageId: "late", CreatedAt: original[10].CreatedAt.Add(time.Millisecond)}
	merged := mergeHistoryMessages(original, []MessageData{changed, late, changed})
	if len(merged) != 101 {
		t.Fatal("update duplicated cached message")
	}
	if merged[11].Id != 101 {
		t.Fatal("late arrival not sorted by its timestamp")
	}
	for _, message := range merged {
		if message.Id == 21 && message.DeliveryStatus != 3 {
			t.Fatal("receipt update lost")
		}
	}
}

func TestSearchFindsOldMessagesAndSkipsDeletedContent(t *testing.T) {
	messages := historyFixture(150)
	messages[0].Text = "Contrato de São José"
	messages[1].Text = "Contrato de São José excluído"
	messages[1].IsDeleted = true
	result := searchHistoryMessages(messages, "sao jose", 0)
	if result.Total != 1 || len(result.Matches) != 1 || result.Matches[0].ID != 1 {
		t.Fatalf("older-message search failed: %+v", result)
	}
	all := searchHistoryMessages(messages, "mensagem", 100)
	if all.Total != 148 || len(all.Matches) != 48 {
		t.Fatalf("search result pagination failed: %+v", all)
	}
}
