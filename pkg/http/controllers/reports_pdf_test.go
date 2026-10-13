package controllers

import (
	"bytes"
	parser "github.com/ledongthuc/pdf"
	"io"
	"sophus/internal/repo"
	"strings"
	"testing"
)

func TestReportPDFContainsUnicodeFiltersAndPaginatedDetails(t *testing.T) {
	report := repo.ConversationReport{
		Filter: repo.ReportFilter{Timezone: "America/Sao_Paulo", AgentID: 2},
		Agents: []repo.ReportOption{{ID: 2, Name: "João"}},
		Totals: repo.ReportMetrics{Sent: 123, Received: 456, Conversations: 100, Unattended: 3},
		Trend:  []repo.ReportTrend{{Label: "01/10", Sent: 20, Received: 30}, {Label: "02/10", Sent: 103, Received: 426}},
	}
	for i := 0; i < 100; i++ {
		report.Rows = append(report.Rows, repo.ReportRow{Connection: "Conexão principal", Department: "Operações", Agent: "João", ReportMetrics: repo.ReportMetrics{Conversations: 1}})
	}
	report.Rows[99].Agent = "Último agente"
	data, err := buildReportPDF(report)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := parser.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("invalid PDF: %v", err)
	}
	if reader.NumPage() < 4 {
		t.Fatalf("expected paginated PDF, got %d pages", reader.NumPage())
	}
	textReader, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(textReader)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Relatório de atendimento", "Todo o histórico", "João", "Operações", "Último agente", "Evolução das mensagens", "123", "456"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("missing %q in PDF", want)
		}
	}
}

func TestEmptyReportPDF(t *testing.T) {
	data, err := buildReportPDF(repo.ConversationReport{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
}
