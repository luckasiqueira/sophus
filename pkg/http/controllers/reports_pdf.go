package controllers

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	"sophus/internal/repo"
	"sophus/internal/reporting"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

func buildReportPDF(report repo.ConversationReport) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.SetTitle("Relatório de atendimento", true)
	pdf.SetAuthor("Sophus", true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Go", "", 8)
		pdf.SetTextColor(110, 110, 120)
		pdf.CellFormat(269, 5, fmt.Sprintf("Sophus · Relatório de atendimento · Página %d", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont("Go", "B", 22)
	pdf.SetTextColor(97, 35, 204)
	pdf.CellFormat(269, 12, "Relatório de atendimento", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 10)
	pdf.SetTextColor(70, 70, 80)
	pdf.MultiCell(269, 6, "Período: "+report.Filter.PeriodLabel()+" · Fuso: "+report.Filter.Timezone+"\nGerado em: "+time.Now().Format("02/01/2006 15:04 MST"), "", "L", false)
	pdf.MultiCell(269, 6, "Conexão: "+reporting.OptionLabel(report.Filter.ConnectionID, report.Connections, "Conexão removida")+"\nFila: "+reporting.OptionLabel(report.Filter.DepartmentID, report.Departments, "Sem fila")+"\nAgente: "+reporting.OptionLabel(report.Filter.AgentID, report.Agents, "Sem agente"), "", "L", false)
	pdf.Ln(4)
	metrics := []reporting.Bar{{"Mensagens enviadas", report.Totals.Sent}, {"Mensagens recebidas", report.Totals.Received}, {"Conversas não atendidas", report.Totals.Unattended}, {"Conversas distintas", report.Totals.Conversations}}
	y := pdf.GetY()
	for i, metric := range metrics {
		x := 14 + float64(i)*68
		pdf.SetFillColor(245, 240, 255)
		pdf.RoundedRect(x, y, 65, 24, 3, "1234", "F")
		pdf.SetXY(x+4, y+3)
		pdf.SetFont("Go", "", 9)
		pdf.Cell(57, 6, metric.Label)
		pdf.SetXY(x+4, y+11)
		pdf.SetFont("Go", "B", 19)
		pdf.Cell(57, 9, strconv.FormatInt(metric.Value, 10))
	}
	pdf.SetY(y + 29)
	pdf.SetFont("Go", "", 8)
	pdf.MultiCell(269, 4, "Mensagens pela data de envio/recebimento. Conversas criadas ou com mensagens no período, contadas uma única vez. Não atendidas: situação atual, sem agente ou em automação, não encerradas. Fila e agente: atribuição atual. Mensagens excluídas não entram nos totais.", "", "L", false)

	// Vector charts stay crisp when printing and require no browser renderer.
	for _, chart := range []struct {
		title string
		bars  []reporting.Bar
	}{
		{"Volume de mensagens", []reporting.Bar{{Label: "Enviadas", Value: report.Totals.Sent}, {Label: "Recebidas", Value: report.Totals.Received}}},
		{"Situação atual das conversas", []reporting.Bar{{Label: "Não atendidas", Value: report.Totals.Unattended}, {Label: "Demais conversas", Value: report.Totals.Conversations - report.Totals.Unattended}}},
		{"Conversas por conexão", reporting.ConversationBars(report, "connection")},
		{"Conversas por fila", reporting.ConversationBars(report, "department")},
		{"Conversas por agente", reporting.ConversationBars(report, "agent")},
	} {
		bars := chart.bars
		if pdf.GetY()+float64(len(bars))*9+20 > 190 {
			pdf.AddPage()
		}
		pdf.Ln(4)
		pdf.SetFont("Go", "B", 12)
		pdf.CellFormat(269, 8, chart.title, "", 1, "L", false, 0, "")
		if len(bars) == 0 {
			pdf.SetFont("Go", "", 9)
			pdf.CellFormat(269, 6, "Sem dados para os filtros selecionados.", "", 1, "L", false, 0, "")
		}
		max := reporting.MaxBar(bars)
		for _, bar := range bars {
			y := pdf.GetY()
			pdf.SetFont("Go", "", 9)
			label := fitPDFLabel(pdf, bar.Label, 73)
			pdf.Cell(76, 8, label)
			pdf.SetFillColor(236, 230, 247)
			pdf.Rect(90, y+2, 170, 4, "F")
			pdf.SetFillColor(124, 58, 237)
			pdf.Rect(90, y+2, reporting.Percent(bar.Value, max)*1.7, 4, "F")
			pdf.SetXY(262, y)
			pdf.CellFormat(20, 8, strconv.FormatInt(bar.Value, 10), "", 1, "R", false, 0, "")
		}
	}
	pdf.AddPage()
	pdf.SetFont("Go", "B", 14)
	pdf.CellFormat(269, 9, "Evolução das mensagens", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 9)
	pdf.CellFormat(269, 6, "Enviadas (roxo) · Recebidas (verde) · Intervalos com atividade", "", 1, "L", false, 0, "")
	if len(report.Trend) == 0 {
		pdf.CellFormat(269, 8, "Nenhuma mensagem no período.", "", 1, "L", false, 0, "")
	} else {
		y := pdf.GetY() + 5
		max := reporting.MaxTrend(report.Trend)
		pdf.SetDrawColor(225, 225, 230)
		for i := 0; i <= 4; i++ {
			lineY := y + 60 - float64(i)*15
			pdf.Line(28, lineY, 278, lineY)
			pdf.SetXY(14, lineY-3)
			pdf.Cell(13, 6, strconv.FormatInt(max*int64(i)/4, 10))
		}
		for series := 0; series < 2; series++ {
			if series == 0 {
				pdf.SetDrawColor(124, 58, 237)
				pdf.SetFillColor(124, 58, 237)
			} else {
				pdf.SetDrawColor(16, 185, 129)
				pdf.SetFillColor(16, 185, 129)
			}
			pdf.SetLineWidth(0.6)
			var lastX, lastY float64
			for i, point := range report.Trend {
				x := 28.0
				if len(report.Trend) > 1 {
					x += float64(i) * 250 / float64(len(report.Trend)-1)
				}
				value := point.Sent
				if series == 1 {
					value = point.Received
				}
				pointY := y + 60 - reporting.Percent(value, max)*0.6
				if i > 0 {
					pdf.Line(lastX, lastY, x, pointY)
				}
				pdf.Circle(x, pointY, 0.8, "F")
				lastX, lastY = x, pointY
			}
		}
		pdf.SetTextColor(70, 70, 80)
		step := (len(report.Trend) + 7) / 8
		for i := 0; i < len(report.Trend); i += step {
			x := 28.0
			if len(report.Trend) > 1 {
				x += float64(i) * 250 / float64(len(report.Trend)-1)
			}
			pdf.SetXY(x-9, y+62)
			pdf.Cell(20, 6, report.Trend[i].Label)
		}
		pdf.SetY(y + 76)
	}
	pdf.Ln(4)
	pdf.SetFont("Go", "B", 13)
	pdf.CellFormat(269, 9, "Detalhamento por conexão, fila e agente", "", 1, "L", false, 0, "")
	widths := []float64{57, 49, 49, 27, 29, 31, 27}
	header := func() {
		pdf.SetFont("Go", "B", 9)
		pdf.SetFillColor(240, 235, 250)
		for i, label := range []string{"Conexão", "Fila", "Agente", "Enviadas", "Recebidas", "Não atendidas", "Conversas"} {
			pdf.CellFormat(widths[i], 8, label, "", 0, "L", true, 0, "")
		}
		pdf.Ln(8)
	}
	header()
	if len(report.Rows) == 0 {
		pdf.SetFont("Go", "", 10)
		pdf.CellFormat(269, 10, "Nenhuma conversa encontrada.", "", 1, "L", false, 0, "")
	}
	for index, row := range report.Rows {
		pdf.SetFont("Go", "", 9)
		pdf.SetFillColor(249, 249, 251)
		values := []string{row.Connection, row.Department, row.Agent, strconv.FormatInt(row.Sent, 10), strconv.FormatInt(row.Received, 10), strconv.FormatInt(row.Unattended, 10), strconv.FormatInt(row.Conversations, 10)}
		rowHeight := 8.0
		for i := 0; i < 3; i++ {
			if height := float64(len(pdf.SplitText(values[i], widths[i]-4)))*5 + 3; height > rowHeight {
				rowHeight = height
			}
		}
		if pdf.GetY()+rowHeight > 190 {
			pdf.AddPage()
			header()
			pdf.SetFont("Go", "", 9)
		}
		x, y := pdf.GetX(), pdf.GetY()
		for i, value := range values {
			align := "L"
			if i >= 3 {
				align = "R"
			}
			if index%2 == 0 {
				pdf.Rect(x, y, widths[i], rowHeight, "F")
			}
			pdf.SetXY(x+2, y+1.5)
			pdf.MultiCell(widths[i]-4, 5, value, "", align, false)
			x += widths[i]
		}
		pdf.SetXY(14, y+rowHeight)
	}
	if pdf.GetY()+9 > 190 {
		pdf.AddPage()
		header()
	}
	pdf.SetFont("Go", "B", 9)
	pdf.SetFillColor(240, 235, 250)
	pdf.CellFormat(155, 9, "Total", "", 0, "L", true, 0, "")
	for i, total := range []int64{report.Totals.Sent, report.Totals.Received, report.Totals.Unattended, report.Totals.Conversations} {
		pdf.CellFormat(widths[i+3], 9, strconv.FormatInt(total, 10), "", 0, "R", true, 0, "")
	}
	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func fitPDFLabel(pdf *fpdf.Fpdf, value string, width float64) string {
	if pdf.GetStringWidth(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && pdf.GetStringWidth(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
