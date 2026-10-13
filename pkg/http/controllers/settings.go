package controllers

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func SaaSSettingsPage(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.SaaSSettings())
}

func SettingsPage(ctx iris.Context) {
	if _, err := middlewares.PlatformAdminIdentifier(ctx); err == nil {
		ctx.RenderComponent(web.SaaSSettings())
		return
	}
	CompanySettingsPage(ctx)
}

func GetSettings(ctx iris.Context) {
	if _, err := middlewares.PlatformAdminIdentifier(ctx); err == nil {
		GetSaaSSettings(ctx)
		return
	}
	GetCompanySettings(ctx)
}

func UpdateSettings(ctx iris.Context) {
	if _, err := middlewares.PlatformAdminIdentifier(ctx); err == nil {
		UpdateSaaSSettings(ctx)
		return
	}
	UpdateCompanySettings(ctx)
}

func GetSaaSSettings(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	settings, err := repo.GetPlatformSettings()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar as configurações da plataforma")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"admin": admin, "settings": settings}})
}

func UpdateSaaSSettings(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	var input repo.UpdatePlatformSettingsInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	input.PaymentProvider = strings.TrimSpace(input.PaymentProvider)
	input.DefaultTimezone = strings.TrimSpace(input.DefaultTimezone)
	if !repo.IsValidPaymentProvider(input.PaymentProvider) {
		writeJSONError(ctx, iris.StatusBadRequest, "provedor de pagamento inválido")
		return
	}
	if !validTimezone(input.DefaultTimezone) {
		writeJSONError(ctx, iris.StatusBadRequest, "fuso horário inválido")
		return
	}
	if input.SubscriptionGraceDays < 0 || input.SubscriptionGraceDays > 90 {
		writeJSONError(ctx, iris.StatusBadRequest, "a tolerância deve ficar entre 0 e 90 dias")
		return
	}
	settings, err := repo.UpdatePlatformSettings(input)
	if err != nil {
		if errors.Is(err, repo.ErrInvalidPaymentProvider) || errors.Is(err, repo.ErrInvalidSubscriptionGraceDays) {
			writeJSONError(ctx, iris.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar as configurações da plataforma")
		return
	}
	ctx.JSON(iris.Map{"data": settings})
}

func UpdateSaaSProfile(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		Name            string `json:"name"`
		Email           string `json:"email"`
		CurrentPassword string `json:"currentPassword"`
		Password        string `json:"password"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Password != "" || body.CurrentPassword != "" {
		writeJSONError(ctx, iris.StatusBadRequest, "a alteração de senha do super admin não está disponível no painel")
		return
	}
	body.Email = admin.Email
	if body.Name == "" || len(body.Name) > 80 {
		writeJSONError(ctx, iris.StatusBadRequest, "nome é obrigatório e deve ter até 80 caracteres")
		return
	}
	if !validEmail(body.Email) || len(body.Email) > 180 {
		writeJSONError(ctx, iris.StatusBadRequest, "email inválido")
		return
	}
	updated, err := repo.UpdatePlatformAdminProfile(admin.Id, repo.UpdatePlatformAdminProfileInput{
		Name: body.Name, Email: body.Email,
	})
	if err != nil {
		if errors.Is(err, repo.ErrAgentEmailInUse) || isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "este email já está em uso")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "administrador não encontrado")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o perfil")
		return
	}
	if updated.SessionVersion != admin.SessionVersion {
		ctx.RemoveCookie(platformCookieName, iris.CookiePath("/"))
	}
	ctx.JSON(iris.Map{"data": updated, "reauthenticate": updated.SessionVersion != admin.SessionVersion})
}

func CompanySettingsPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.CompanySettings())
}

func GetCompanySettings(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	settings, err := repo.GetCompanySettings(agent.CompanyId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar as configurações da empresa")
		return
	}
	ctx.JSON(iris.Map{"data": settings})
}

func UpdateCompanySettings(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var input repo.UpdateCompanySettingsInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = normalizeEmail(input.Email)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.PaymentProviderOverride != nil {
		value := strings.TrimSpace(*input.PaymentProviderOverride)
		if value == "" {
			input.PaymentProviderOverride = nil
		} else {
			input.PaymentProviderOverride = &value
		}
	}
	if input.Name == "" || len(input.Name) > 160 {
		writeJSONError(ctx, iris.StatusBadRequest, "nome é obrigatório e deve ter até 160 caracteres")
		return
	}
	if !validEmail(input.Email) || len(input.Email) > 180 {
		writeJSONError(ctx, iris.StatusBadRequest, "email inválido")
		return
	}
	if !validTimezone(input.Timezone) {
		writeJSONError(ctx, iris.StatusBadRequest, "fuso horário inválido")
		return
	}
	if input.PaymentProviderOverride != nil && !repo.IsValidPaymentProvider(*input.PaymentProviderOverride) {
		writeJSONError(ctx, iris.StatusBadRequest, "provedor de pagamento inválido")
		return
	}
	if !repo.IsValidMediaRetention(input.MediaRetentionDays) {
		writeJSONError(ctx, iris.StatusBadRequest, "retenção de mídia inválida")
		return
	}
	operations := input.Operations
	if !repo.ValidMessageEditPolicy(operations.MessageEditPolicy) {
		writeJSONError(ctx, iris.StatusBadRequest, "permissão de edição de mensagens inválida")
		return
	}
	_, startErr := time.Parse("15:04", operations.BusinessHoursStart)
	_, endErr := time.Parse("15:04", operations.BusinessHoursEnd)
	_, lunchStartErr := time.Parse("15:04", operations.BusinessHoursLunchStart)
	_, lunchEndErr := time.Parse("15:04", operations.BusinessHoursLunchEnd)
	if len(operations.MessageSignatureTemplate) > 160 || len(operations.OutsideHoursMessage) > 1000 || len(operations.WelcomeMessage) > 1000 ||
		operations.AutoCloseMinutes < 0 || operations.AutoCloseMinutes > 43200 || operations.ConversationRetentionDays < 0 || operations.ConversationRetentionDays > 3650 ||
		operations.MaxAttachmentMB < 1 || operations.MaxAttachmentMB > 50 ||
		(operations.AutoAssignStrategy != "least_loaded" && operations.AutoAssignStrategy != "random") || startErr != nil || endErr != nil || lunchStartErr != nil || lunchEndErr != nil || !validBusinessDays(operations.BusinessHoursDays) || !validAttachmentTypes(operations.AllowedAttachmentTypes) {
		writeJSONError(ctx, iris.StatusBadRequest, "configurações operacionais inválidas")
		return
	}
	settings, err := repo.UpdateCompanySettings(agent.CompanyId, input)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrCompanyPaymentOverrideNotAllowed):
			writeJSONError(ctx, iris.StatusConflict, "a plataforma não permite escolher um provedor diferente")
		case errors.Is(err, repo.ErrInvalidPaymentProvider):
			writeJSONError(ctx, iris.StatusBadRequest, "provedor de pagamento inválido")
		case errors.Is(err, repo.ErrInvalidMediaRetention):
			writeJSONError(ctx, iris.StatusBadRequest, "retenção de mídia inválida")
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
		case isUniqueViolation(err):
			writeJSONError(ctx, iris.StatusConflict, "este email já está cadastrado por outra empresa")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar as configurações da empresa")
		}
		return
	}
	ctx.JSON(iris.Map{"data": settings})
}

func validBusinessDays(values []int) bool {
	if len(values) == 0 || len(values) > 7 {
		return false
	}
	seen := map[int]bool{}
	for _, value := range values {
		if value < 0 || value > 6 || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validAttachmentTypes(values []string) bool {
	if len(values) == 0 || len(values) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] || (value != "image" && value != "video" && value != "audio" && value != "document") {
			return false
		}
		seen[value] = true
	}
	return true
}
