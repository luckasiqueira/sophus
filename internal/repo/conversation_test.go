package repo

import "testing"

func TestConversationListFilter(t *testing.T) {
	tests := []struct {
		tab  string
		want string
	}{
		{tab: "active", want: `cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending', 'running')`},
		{tab: "pending", want: `(cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'`},
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
