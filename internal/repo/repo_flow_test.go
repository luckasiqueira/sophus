package repo

import "testing"

func TestConversationStatusForFinalizedExecution(t *testing.T) {
	tests := []struct {
		executionStatus string
		want            string
	}{
		{executionStatus: "completed", want: ConversationStatusPending},
		{executionStatus: "failed", want: ConversationStatusPending},
	}

	for _, test := range tests {
		got := conversationStatusAfterExecution(test.executionStatus)
		if got != test.want {
			t.Fatalf("status for %q = %q, want %q", test.executionStatus, got, test.want)
		}
	}
}
