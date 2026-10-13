package reporting

import (
	"fmt"
	"sophus/internal/repo"
	"testing"
)

func TestConversationBarsPreserveTotalsWhenGroupingOthers(t *testing.T) {
	var report repo.ConversationReport
	for i := 1; i <= 12; i++ {
		report.Rows = append(report.Rows, repo.ReportRow{Agent: fmt.Sprintf("Agent %02d", i), ReportMetrics: repo.ReportMetrics{Conversations: int64(i)}})
	}
	report.Rows = append(report.Rows, repo.ReportRow{Agent: "Agent 12", ReportMetrics: repo.ReportMetrics{Conversations: 5}})
	bars := ConversationBars(report, "agent")
	if len(bars) != 9 || bars[0].Value != 17 || bars[8].Label != "Outros" {
		t.Fatalf("bars: %+v", bars)
	}
	var total int64
	for _, bar := range bars {
		total += bar.Value
	}
	if total != 83 {
		t.Fatalf("total = %d, want 83", total)
	}
	if Percent(1, 0) != 0 || Percent(25, 100) != 25 {
		t.Fatal("invalid percentage")
	}
}
