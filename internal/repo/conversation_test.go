package repo

import "testing"

func TestConversationListFilter(t *testing.T) {
	tests := []struct {
		tab  string
		want string
	}{
		{tab: "active", want: `cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending')`},
		{tab: "pending", want: `cv."agentId" IS NULL AND cv.status NOT IN ('closed', 'running')`},
		{tab: "closed", want: `cv.status = 'closed'`},
	}

	for _, test := range tests {
		t.Run(test.tab, func(t *testing.T) {
			if got := conversationListFilter(test.tab); got != test.want {
				t.Fatalf("conversationListFilter(%q) = %q, want %q", test.tab, got, test.want)
			}
		})
	}
}
