package repo

import "testing"

func TestClampOverviewPage(t *testing.T) {
	tests := []struct {
		name     string
		page     int
		total    int
		pageSize int
		want     int
	}{
		{name: "keeps valid page", page: 2, total: 120, pageSize: 50, want: 2},
		{name: "clamps after deletion", page: 3, total: 50, pageSize: 50, want: 1},
		{name: "empty list uses first page", page: 4, total: 0, pageSize: 50, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := clampOverviewPage(test.page, test.total, test.pageSize); got != test.want {
				t.Fatalf("clampOverviewPage(%d, %d, %d) = %d, want %d", test.page, test.total, test.pageSize, got, test.want)
			}
		})
	}
}
