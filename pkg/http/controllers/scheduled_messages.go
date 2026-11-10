package controllers

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

const (
	scheduledMessageMinLead = time.Minute
	scheduledMessageMaxLead = 366 * 24 * time.Hour
)

var scheduledMessageStatusFilters = map[string][]string{
	"":         nil,
	"upcoming": {repo.ScheduledMessageStatusPending, repo.ScheduledMessageStatusProcessing},
	"sent":     {repo.ScheduledMessageStatusSent},
	"failed":   {repo.ScheduledMessageStatusFailed},
	"canceled": {repo.ScheduledMessageStatusCanceled},
}

func ScheduledMessagesPage(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.RenderComponent(web.ScheduledMessages(scheduleLocation(settings.Timezone).String(), agent.IsAdmin(), scheduleAttachmentPolicy(settings), scheduleDraftFromQuery(ctx, agent)))
}

// scheduleDraftFromQuery opens the form for the contact of the conversation
// the agent came from (/schedules?contactId=..&connectionId=..). Only ids
// travel in the URL; the contact is loaded and checked against the company.
func scheduleDraftFromQuery(ctx iris.Context, agent repo.Agent) *web.ScheduleDraft {
	contactID, err := strconv.Atoi(ctx.URLParam("contactId"))
	if err != nil || contactID <= 0 {
		return nil
	}
	contact, err := repo.GetContactById(contactID)
	if err != nil || contact.CompanyId != agent.CompanyId || contact.IsGroup || contact.IsBlocked {
		return nil
	}
	draft := &web.ScheduleDraft{ContactId: contact.Id, ContactName: contact.Name, ContactNumber: contact.Number}
	if connectionID, err := strconv.Atoi(ctx.URLParam("connectionId")); err == nil && connectionID > 0 {
		draft.ConnectionId = connectionID
	}
	return draft
}

func scheduleAttachmentPolicy(settings repo.CompanyOperationSettings) web.ScheduleAttachmentPolicy {
	policy := web.ScheduleAttachmentPolicy{AllowedTypes: []string{}, MaxBytes: map[string]int64{}}
	companyLimit := int64(settings.MaxAttachmentMB) << 20
	for _, kind := range settings.AllowedAttachmentTypes {
		limit := media.MaxSize(kind)
		if limit <= 0 {
			continue
		}
		policy.AllowedTypes = append(policy.AllowedTypes, kind)
		policy.MaxBytes[kind] = min(limit, companyLimit)
	}
	return policy
}

func ListScheduledMessagesAPI(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	statuses, ok := scheduledMessageStatusFilters[ctx.URLParam("status")]
	if !ok {
		writeJSONError(ctx, iris.StatusBadRequest, "filtro de status inválido")
		return
	}
	page, _ := strconv.Atoi(ctx.URLParamDefault("page", "1"))
	filter := repo.ScheduledMessageFilter{CompanyId: agent.CompanyId, Statuses: statuses, Page: page, PageSize: 30}
	if !agent.IsAdmin() {
		filter.AgentId = &agent.Id
	}
	result, err := repo.ListScheduledMessages(filter)
	if err != nil {
		log.Printf("scheduled messages list failed: company=%d error=%v", agent.CompanyId, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar os agendamentos")
		return
	}
	counts, err := repo.ScheduledMessageCounts(agent.CompanyId, filter.AgentId)
	if err != nil {
		log.Printf("scheduled messages count failed: company=%d error=%v", agent.CompanyId, err)
		counts = map[string]int{}
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(iris.Map{"data": result.Data, "total": result.Total, "page": result.Page, "pageSize": result.PageSize, "counts": counts})
}

func SendScheduledMessageNowAPI(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	id, err := ctx.Params().GetInt64("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "agendamento inválido")
		return
	}
	message, err := repo.SendScheduledMessageNow(agent.CompanyId, id, agent.Id, agent.IsAdmin())
	if err != nil {
		writeScheduledMessageError(ctx, err, "não foi possível antecipar o envio")
		return
	}
	if agent.IsSupport {
		recordCompanyAction(ctx, agent, "send_now", "scheduled_message", strconv.FormatInt(id, 10))
	}
	ctx.JSON(iris.Map{"data": message})
}

func CreateScheduledMessageAPI(ctx iris.Context) {
	agent, settings, ok := scheduledMessageActor(ctx)
	if !ok {
		return
	}
	input, ok := readScheduledMessageForm(ctx, settings)
	if !ok {
		return
	}
	uploaded, ok := storeScheduledMessageMedia(ctx, agent.CompanyId, settings)
	if !ok {
		return
	}
	input.Media = uploaded
	if strings.TrimSpace(input.Body) == "" && uploaded == nil {
		writeJSONError(ctx, iris.StatusBadRequest, "escreva uma mensagem ou anexe um arquivo")
		return
	}
	message, err := repo.CreateScheduledMessage(agent.CompanyId, agent.Id, input)
	if err != nil {
		releaseScheduledMessageMedia(agent.CompanyId, uploaded)
		writeScheduledMessageError(ctx, err, "não foi possível criar o agendamento")
		return
	}
	if agent.IsSupport {
		recordCompanyAction(ctx, agent, "create", "scheduled_message", strconv.FormatInt(message.Id, 10))
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": message})
}

func UpdateScheduledMessageAPI(ctx iris.Context) {
	agent, settings, ok := scheduledMessageActor(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt64("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "agendamento inválido")
		return
	}
	input, ok := readScheduledMessageForm(ctx, settings)
	if !ok {
		return
	}
	uploaded, ok := storeScheduledMessageMedia(ctx, agent.CompanyId, settings)
	if !ok {
		return
	}
	input.Media = uploaded
	input.RemoveMedia = uploaded == nil && ctx.FormValue("removeMedia") == "true"
	message, released, err := repo.UpdateScheduledMessage(agent.CompanyId, id, agent.Id, agent.IsAdmin(), input)
	if err != nil {
		releaseScheduledMessageMedia(agent.CompanyId, uploaded)
		writeScheduledMessageError(ctx, err, "não foi possível salvar o agendamento")
		return
	}
	releaseScheduledMessageMedia(agent.CompanyId, released)
	if agent.IsSupport {
		recordCompanyAction(ctx, agent, "update", "scheduled_message", strconv.FormatInt(id, 10))
	}
	ctx.JSON(iris.Map{"data": message})
}

func CancelScheduledMessageAPI(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	id, err := ctx.Params().GetInt64("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "agendamento inválido")
		return
	}
	released, err := repo.CancelScheduledMessage(agent.CompanyId, id, agent.Id, agent.IsAdmin())
	if err != nil {
		writeScheduledMessageError(ctx, err, "não foi possível cancelar o agendamento")
		return
	}
	releaseScheduledMessageMedia(agent.CompanyId, released)
	if agent.IsSupport {
		recordCompanyAction(ctx, agent, "cancel", "scheduled_message", strconv.FormatInt(id, 10))
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func scheduledMessageActor(ctx iris.Context) (repo.Agent, repo.CompanyOperationSettings, bool) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return repo.Agent{}, repo.CompanyOperationSettings{}, false
	}
	settings, err := repo.GetCompanyOperationSettings(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar as configurações da empresa")
		return repo.Agent{}, repo.CompanyOperationSettings{}, false
	}
	return agent, settings, true
}

func scheduleLocation(timezone string) *time.Location {
	if location, err := time.LoadLocation(strings.TrimSpace(timezone)); err == nil && timezone != "" {
		return location
	}
	if location, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return location
	}
	return time.UTC
}

// parseScheduleTime reads the date and time typed in the company's timezone.
func parseScheduleTime(date, clock string, location *time.Location, now time.Time) (time.Time, string) {
	value, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(date)+" "+strings.TrimSpace(clock), location)
	if err != nil {
		return time.Time{}, "informe uma data e um horário válidos"
	}
	if value.Before(now.Add(scheduledMessageMinLead)) {
		return time.Time{}, "escolha um horário pelo menos 1 minuto à frente"
	}
	if value.After(now.Add(scheduledMessageMaxLead)) {
		return time.Time{}, "o agendamento pode ser feito para até 1 ano à frente"
	}
	return value, ""
}

func readScheduledMessageForm(ctx iris.Context, settings repo.CompanyOperationSettings) (repo.ScheduledMessageInput, bool) {
	contactID, contactErr := strconv.Atoi(ctx.FormValue("contactId"))
	connectionID, connectionErr := strconv.Atoi(ctx.FormValue("connectionId"))
	if contactErr != nil || connectionErr != nil || contactID <= 0 || connectionID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "selecione o contato e a conexão de envio")
		return repo.ScheduledMessageInput{}, false
	}
	body := strings.TrimSpace(ctx.FormValue("body"))
	if len([]rune(body)) > repo.ScheduledMessageBodyLimit {
		writeJSONError(ctx, iris.StatusBadRequest, "a mensagem deve ter até 4096 caracteres")
		return repo.ScheduledMessageInput{}, false
	}
	scheduledFor, problem := parseScheduleTime(ctx.FormValue("date"), ctx.FormValue("time"), scheduleLocation(settings.Timezone), time.Now())
	if problem != "" {
		writeJSONError(ctx, iris.StatusBadRequest, problem)
		return repo.ScheduledMessageInput{}, false
	}
	recurrence := strings.TrimSpace(ctx.FormValue("recurrence"))
	if recurrence == "" {
		recurrence = repo.ScheduledRecurrenceNone
	}
	if !repo.ValidScheduledRecurrence(recurrence) {
		writeJSONError(ctx, iris.StatusBadRequest, "escolha uma repetição válida")
		return repo.ScheduledMessageInput{}, false
	}
	input := repo.ScheduledMessageInput{ContactId: contactID, ConnectionId: connectionID, Body: body, ScheduledFor: scheduledFor, Recurrence: recurrence}
	if raw := strings.TrimSpace(ctx.FormValue("recurrenceUntil")); raw != "" && recurrence != repo.ScheduledRecurrenceNone {
		until, problem := parseRecurrenceUntil(raw, scheduledFor, scheduleLocation(settings.Timezone))
		if problem != "" {
			writeJSONError(ctx, iris.StatusBadRequest, problem)
			return repo.ScheduledMessageInput{}, false
		}
		input.RecurrenceUntil = &until
	}
	return input, true
}

// parseRecurrenceUntil reads the last day a series may repeat on; it must not
// come before the first send and is capped at two years.
func parseRecurrenceUntil(value string, first time.Time, location *time.Location) (time.Time, string) {
	until, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, "informe uma data final de repetição válida"
	}
	firstDay := first.In(location)
	firstDay = time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, location)
	if until.Before(firstDay) {
		return time.Time{}, "a repetição deve terminar depois do primeiro envio"
	}
	if until.After(firstDay.AddDate(2, 0, 0)) {
		return time.Time{}, "a repetição pode durar até 2 anos"
	}
	return until, ""
}

// storeScheduledMessageMedia saves the optional upload in the media library,
// following the same policy and storage accounting as chat attachments.
func storeScheduledMessageMedia(ctx iris.Context, companyID int, settings repo.CompanyOperationSettings) (*repo.ScheduledMessageMedia, bool) {
	file, header, err := ctx.FormFile("file")
	if errors.Is(err, http.ErrMissingFile) || (err == nil && header == nil) {
		return nil, true
	}
	if err != nil || header.Size <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "selecione um arquivo válido")
		return nil, false
	}
	defer file.Close()
	kind := strings.TrimSpace(ctx.FormValue("kind"))
	if !operatorMediaAllowed(kind, header.Size, settings) {
		writeJSONError(ctx, iris.StatusForbidden, "este arquivo não atende à política de anexos da empresa")
		return nil, false
	}
	if err := repo.ReserveCompanyStorage(companyID, header.Size); err != nil {
		if errors.Is(err, repo.ErrStorageLimitReached) {
			writeJSONError(ctx, iris.StatusInsufficientStorage, "o limite de armazenamento da empresa foi atingido")
			return nil, false
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível reservar espaço para o arquivo")
		return nil, false
	}
	stored, err := media.SaveScoped(companyID, media.ScopeMessages, kind, header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		_ = repo.ReleaseCompanyStorageReservation(companyID, header.Size)
		writeJSONError(ctx, iris.StatusBadRequest, err.Error())
		return nil, false
	}
	if err := repo.CompleteCompanyStorageReservation(companyID, header.Size, stored.Size); err != nil {
		_, _ = media.Delete(companyID, media.ScopeMessages, stored.Path)
		_ = repo.ReleaseCompanyStorageReservation(companyID, header.Size)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível contabilizar o arquivo")
		return nil, false
	}
	return &repo.ScheduledMessageMedia{Kind: kind, Path: stored.Path, Name: stored.Name, Size: stored.Size}, true
}

// releaseScheduledMessageMedia deletes an attachment no schedule or message
// uses anymore and returns its space to the company quota.
func releaseScheduledMessageMedia(companyID int, attachment *repo.ScheduledMessageMedia) {
	if attachment == nil || attachment.Path == "" {
		return
	}
	scheduled, err := repo.CountScheduledMessageMediaReferences(companyID, attachment.Path)
	if err != nil || scheduled > 0 {
		return
	}
	sent, err := repo.CountMessageMediaReferences(companyID, media.ScopeMessages, attachment.Path)
	if err != nil || sent > 0 {
		return
	}
	size, err := media.Delete(companyID, media.ScopeMessages, attachment.Path)
	if err != nil {
		log.Printf("scheduled message media cleanup failed: company=%d file=%s error=%v", companyID, attachment.Path, err)
		return
	}
	if err := repo.DecrementCompanyStorage(companyID, size); err != nil {
		log.Printf("scheduled message storage release failed: company=%d file=%s error=%v", companyID, attachment.Path, err)
	}
}

func writeScheduledMessageError(ctx iris.Context, err error, fallback string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, iris.StatusNotFound, "agendamento não encontrado")
	case errors.Is(err, repo.ErrScheduledMessageTarget):
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "o contato ou a conexão não estão disponíveis para agendamento")
	case errors.Is(err, repo.ErrScheduledMessageEmpty):
		writeJSONError(ctx, iris.StatusBadRequest, "escreva uma mensagem ou anexe um arquivo")
	case errors.Is(err, repo.ErrScheduledMessageLocked):
		writeJSONError(ctx, iris.StatusConflict, "este agendamento já foi enviado ou cancelado e não pode mais ser alterado")
	default:
		log.Printf("scheduled message request failed: %v", err)
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
}
