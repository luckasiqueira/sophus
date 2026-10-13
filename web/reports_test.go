package web

import (
	"bytes"
	"context"
	"sophus/internal/repo"
	"strings"
	"testing"
	"time"
)

func TestReportsRendering(t *testing.T) {
	for _, test := range []struct {
		name   string
		report repo.ConversationReport
		err    string
		want   []string
		absent []string
	}{
		{name: "empty", want: []string{"Nenhuma conversa encontrada", "Mensagens enviadas", `action="/reports"`}},
		{name: "failure", err: "Falha ao carregar", want: []string{`role="alert"`, "Falha ao carregar"}, absent: []string{"Nenhuma conversa encontrada", "Mensagens enviadas"}},
		{name: "data and escaping", report: repo.ConversationReport{
			Filter: repo.ReportFilter{AgentID: 7}, Agents: []repo.ReportOption{{ID: 7, Name: "Ana"}},
			Totals: repo.ReportMetrics{Sent: 20, Received: 30, Conversations: 2},
			Rows:   []repo.ReportRow{{Connection: "<script>alert(1)</script>", Department: "Suporte", Agent: "Ana", ReportMetrics: repo.ReportMetrics{Sent: 20, Received: 30, Conversations: 2}}},
		}, want: []string{`value="7" selected`, "&lt;script&gt;", "Suporte", ">20<", ">30<"}, absent: []string{"<script>alert(1)</script>", "Nenhuma conversa encontrada"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Reports(test.report, test.err).Render(context.Background(), &buf); err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(buf.String(), absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
		})
	}
}

func TestReportsRenderPeriodChartsAndFilteredPDFLink(t *testing.T) {
	report := repo.ConversationReport{
		Filter: repo.ReportFilter{Period: "custom", Timezone: "America/Sao_Paulo", AgentID: 9, From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)},
		Totals: repo.ReportMetrics{Sent: 10, Received: 20, Conversations: 2, Unattended: 1},
		Trend:  []repo.ReportTrend{{Label: "01/10", Sent: 10, Received: 20}},
	}
	var buf bytes.Buffer
	if err := Reports(report, "").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-report-select`, `value="custom" selected`, `value="2026-10-01"`, `value="2026-10-02"`, "01/10/2026 a 02/10/2026", "/reports/pdf?", "agent=9", "from=2026-10-01", "to=2026-10-02", "Evolução das mensagens", "Conversas por fila", "Situação atual das conversas", "01/10 · Recebidas: 20"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
