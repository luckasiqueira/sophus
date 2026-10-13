package controllers

import (
	"fmt"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"
	"strconv"
	"time"
)

func parseReportFilter(connection, department, agent string) (repo.ReportFilter, error) {
	var filter repo.ReportFilter
	for _, field := range []struct {
		value  string
		target *int
	}{
		{connection, &filter.ConnectionID}, {department, &filter.DepartmentID}, {agent, &filter.AgentID},
	} {
		if field.value == "" {
			continue
		}
		id, err := strconv.Atoi(field.value)
		if err != nil || id < -1 {
			return filter, fmt.Errorf("filtro inválido")
		}
		*field.target = id
	}
	return filter, nil
}

func ReportsPage(ctx iris.Context) {
	report, ok := loadReport(ctx)
	if !ok {
		return
	}
	ctx.RenderComponent(web.Reports(report, ""))
}

func parseReportPeriod(filter repo.ReportFilter, period, fromValue, toValue string, now time.Time, location *time.Location) (repo.ReportFilter, error) {
	filter.Timezone = location.String()
	if period == "" {
		period = "all"
	}
	filter.Period = period
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	switch period {
	case "all":
		return filter, nil
	case "today":
		filter.From, filter.To = today, today.AddDate(0, 0, 1)
	case "7", "30":
		days, _ := strconv.Atoi(period)
		filter.From, filter.To = today.AddDate(0, 0, 1-days), today.AddDate(0, 0, 1)
	case "month":
		filter.From = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, location)
		filter.To = today.AddDate(0, 0, 1)
	case "custom":
		from, fromErr := time.ParseInLocation("2006-01-02", fromValue, location)
		to, toErr := time.ParseInLocation("2006-01-02", toValue, location)
		if fromErr != nil || toErr != nil || to.Before(from) {
			return filter, fmt.Errorf("período inválido")
		}
		filter.From, filter.To = from, to.AddDate(0, 0, 1)
	default:
		return filter, fmt.Errorf("período inválido")
	}
	return filter, nil
}

func loadReport(ctx iris.Context) (repo.ConversationReport, bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return repo.ConversationReport{}, false
	}
	filter, err := parseReportFilter(ctx.URLParam("connection"), ctx.URLParam("department"), ctx.URLParam("agent"))
	if err != nil {
		ctx.StopWithText(iris.StatusBadRequest, "Filtros de relatório inválidos.")
		return repo.ConversationReport{}, false
	}
	company, err := repo.GetCompanyById(agent.CompanyId)
	if err != nil {
		ctx.StopWithText(iris.StatusInternalServerError, "Não foi possível consultar a empresa.")
		return repo.ConversationReport{}, false
	}
	location, err := time.LoadLocation(company.Timezone)
	if err != nil {
		ctx.StopWithText(iris.StatusInternalServerError, "Fuso horário da empresa inválido.")
		return repo.ConversationReport{}, false
	}
	filter, err = parseReportPeriod(filter, ctx.URLParam("period"), ctx.URLParam("from"), ctx.URLParam("to"), time.Now(), location)
	if err != nil {
		ctx.StopWithText(iris.StatusBadRequest, "Período inválido. Informe a data inicial e final em ordem cronológica.")
		return repo.ConversationReport{}, false
	}
	ctx.Header("Cache-Control", "no-store")
	report, err := repo.GetConversationReport(ctx.Request().Context(), agent, filter)
	if err != nil {
		ctx.Application().Logger().Errorf("load conversation report: %v", err)
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.RenderComponent(web.Reports(report, "Não foi possível carregar o relatório. Tente novamente."))
		return report, false
	}
	return report, true
}

func ExportReportPDF(ctx iris.Context) {
	report, ok := loadReport(ctx)
	if !ok {
		return
	}
	data, err := buildReportPDF(report)
	if err != nil {
		ctx.Application().Logger().Errorf("export conversation report: %v", err)
		ctx.StopWithText(iris.StatusInternalServerError, "Não foi possível exportar o relatório.")
		return
	}
	ctx.ContentType("application/pdf")
	ctx.Header("Content-Disposition", `attachment; filename="relatorio-atendimento.pdf"`)
	_, _ = ctx.Write(data)
}
