package controllers

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func CompanyOverviewPage(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	if _, ok := routeID(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.CompanyOverview())
}

func GetCompanyOperationalOverview(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	company, err := repo.GetCompanyById(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar a empresa")
		return
	}
	location, err := time.LoadLocation(company.Timezone)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "fuso horário da empresa inválido")
		return
	}
	now := time.Now().In(location)
	defaultFrom := now.AddDate(0, 0, -29).Format("2006-01-02")
	defaultTo := now.Format("2006-01-02")
	fromValue := strings.TrimSpace(ctx.URLParam("from"))
	toValue := strings.TrimSpace(ctx.URLParam("to"))
	if fromValue == "" {
		fromValue = defaultFrom
	}
	if toValue == "" {
		toValue = defaultTo
	}
	from, fromErr := time.ParseInLocation("2006-01-02", fromValue, location)
	toDate, toErr := time.ParseInLocation("2006-01-02", toValue, location)
	if fromErr != nil || toErr != nil || toDate.Before(from) || toDate.After(from.AddDate(0, 0, 365)) {
		writeJSONError(ctx, iris.StatusBadRequest, "período inválido; selecione no máximo 366 dias")
		return
	}
	toExclusive := toDate.AddDate(0, 0, 1)
	includeTotals := !strings.EqualFold(strings.TrimSpace(ctx.URLParam("includeTotals")), "false")
	includeMessages := !strings.EqualFold(strings.TrimSpace(ctx.URLParam("includeMessages")), "false")
	connectionPage := overviewPageParam(ctx.URLParam("connectionsPage"))
	flowPage := overviewPageParam(ctx.URLParam("flowsPage"))
	userPage := overviewPageParam(ctx.URLParam("usersPage"))
	overview, err := repo.GetCompanyOperationalOverview(id, from.UTC(), toExclusive.UTC(), includeMessages, includeTotals,
		connectionPage, flowPage, userPage)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar a visão da empresa")
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(iris.Map{"data": overview, "period": iris.Map{"from": fromValue, "to": toValue},
		"messagesIncluded": includeMessages, "totalsIncluded": includeMessages && includeTotals})
}

func overviewPageParam(value string) int {
	page, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || page < 1 || page > 100000 {
		return 1
	}
	return page
}
