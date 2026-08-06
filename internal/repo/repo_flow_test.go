package repo

import "testing"

func TestConversationStatusForFinalizedExecution(t *testing.T) {
	tests := []struct {
		executionStatus string
		want            string
	}{
		{executionStatus: "completed", want: ConversationStatusCompleted},
		{executionStatus: "failed", want: ConversationStatusOpen},
	}

	for _, test := range tests {
		got := conversationStatusAfterExecution(test.executionStatus)
		if got != test.want {
			t.Fatalf("status for %q = %q, want %q", test.executionStatus, got, test.want)
		}
	}
}
