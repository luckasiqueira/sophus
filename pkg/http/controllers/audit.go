package controllers

import (
	"log"
	"strconv"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

// AuditCompanyAction records only successful administrative mutations, without request bodies.
func AuditCompanyAction(action, target string, handler iris.Handler) iris.Handler {
	return func(ctx iris.Context) {
		agent, err := middlewares.AgentIdentifier(ctx)
		if err != nil {
			ctx.StopWithStatus(iris.StatusUnauthorized)
			return
		}
		id := ctx.Params().Get("id")
		// Resolve the label first: a delete removes the row that names it.
		name := repo.AuditTargetName(agent.CompanyId, target, id)
		handler(ctx)
		status := ctx.ResponseWriter().StatusCode()
		if status < 200 || status >= 300 {
			return
		}
		recordNamedCompanyAction(ctx, agent, action, target, id, name)
	}
}

func recordCompanyAction(ctx iris.Context, agent repo.Agent, action, target, id string) {
	recordNamedCompanyAction(ctx, agent, action, target, id, repo.AuditTargetName(agent.CompanyId, target, id))
}

func recordNamedCompanyAction(ctx iris.Context, agent repo.Agent, action, target, id, name string) {
	event := companyActionAuditEvent(ctx, agent, action, target, id)
	event.TargetName = name
	if err := repo.RecordAuditEvent(event); err != nil {
		log.Printf("audit event failed: action=%s company=%d: %v", action, agent.CompanyId, err)
	}
}

func companyActionAuditEvent(ctx iris.Context, agent repo.Agent, action, target, id string) repo.AuditEvent {
	event := repo.AuditEvent{
		CompanyID: &agent.CompanyId, ActorID: &agent.Id, ActorEmail: agent.Email,
		ActorType: "agent", Action: action, TargetType: target, TargetID: id, IPAddress: ctx.RemoteAddr(),
	}
	if session, support := middlewares.SupportSession(ctx); support {
		event.ActorID = &session.Admin.Id
		event.ActorEmail = session.Admin.Email
		event.ActorType = "platform_support"
	}
	return event
}

func ListAuditEvents(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	var companyID, actorID *int
	for _, filter := range []struct {
		key  string
		dest **int
	}{{"companyId", &companyID}, {"actorId", &actorID}} {
		value := strings.TrimSpace(ctx.URLParam(filter.key))
		if value == "" {
			continue
		}
		id, err := strconv.Atoi(value)
		if err != nil || id <= 0 {
			writeJSONError(ctx, iris.StatusBadRequest, "filtro inválido")
			return
		}
		*filter.dest = &id
	}
	page := 1
	if value := ctx.URLParam("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100000 {
			writeJSONError(ctx, iris.StatusBadRequest, "página inválida")
			return
		}
		page = parsed
	}
	result, err := repo.ListAuditEvents(companyID, actorID, ctx.URLParam("email"), page)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os logs")
		return
	}
	ctx.JSON(result)
}

// ListCompanyAuditEvents shows a company admin the audit trail of their own company only.
func ListCompanyAuditEvents(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	filter := repo.CompanyAuditFilter{Category: strings.TrimSpace(ctx.URLParam("category")), Actor: ctx.URLParam("actor")}
	if !repo.ValidCompanyAuditCategory(filter.Category) || len([]rune(filter.Actor)) > 120 {
		writeJSONError(ctx, iris.StatusBadRequest, "filtro inválido")
		return
	}
	for _, bound := range []struct {
		key  string
		dest **time.Time
	}{{"from", &filter.From}, {"to", &filter.To}} {
		value := strings.TrimSpace(ctx.URLParam(bound.key))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeJSONError(ctx, iris.StatusBadRequest, "período inválido")
			return
		}
		*bound.dest = &parsed
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		writeJSONError(ctx, iris.StatusBadRequest, "período inválido")
		return
	}
	page := 1
	if value := ctx.URLParam("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100000 {
			writeJSONError(ctx, iris.StatusBadRequest, "página inválida")
			return
		}
		page = parsed
	}
	result, err := repo.ListCompanyAuditEvents(agent.CompanyId, filter, page)
	if err != nil {
		log.Printf("company audit list failed: company=%d error=%v", agent.CompanyId, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os logs")
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(result)
}
