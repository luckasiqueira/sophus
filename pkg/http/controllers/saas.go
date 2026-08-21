package controllers

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const platformCookieName = "platform_token"

func SaaSSetupPage(ctx iris.Context) {
	count, err := repo.CountPlatformAdmins()
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível verificar a configuração da plataforma"})
		return
	}
	if count != 0 {
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	if strings.TrimSpace(env.Backend["SAAS_SETUP_TOKEN"]) == "" {
		ctx.StopWithJSON(iris.StatusServiceUnavailable, iris.Map{"error": "SAAS_SETUP_TOKEN não configurado"})
		return
	}
	ctx.RenderComponent(web.SaaSSetup())
}

func CreateSaaSSetup(ctx iris.Context) {
	name := strings.TrimSpace(ctx.FormValue("name"))
	email := normalizeEmail(ctx.FormValue("email"))
	password := ctx.FormValue("password")
	setupToken := ctx.FormValue("setupToken")
	expectedSetupToken := strings.TrimSpace(env.Backend["SAAS_SETUP_TOKEN"])
	if expectedSetupToken == "" || subtle.ConstantTimeCompare([]byte(setupToken), []byte(expectedSetupToken)) != 1 {
		writeJSONError(ctx, iris.StatusUnauthorized, "token de configuração inválido")
		return
	}
	if name == "" || len(name) > 80 {
		writeJSONError(ctx, iris.StatusBadRequest, "nome é obrigatório e deve ter até 80 caracteres")
		return
	}
	if !validEmail(email) {
		writeJSONError(ctx, iris.StatusBadRequest, "email inválido")
		return
	}
	if len(password) < 8 {
		writeJSONError(ctx, iris.StatusBadRequest, "a senha deve ter pelo menos 8 caracteres")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível proteger a senha")
		return
	}
	_, err = repo.CreateFirstPlatformAdmin(repo.CreatePlatformAdminInput{
		Name:     name,
		Email:    email,
		Password: string(passwordHash),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repo.ErrAgentEmailInUse) || isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "a configuração inicial já foi concluída")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível criar o administrador da plataforma")
		return
	}
	redirectRequest(ctx, "/login")
}

func SaaSDashboardPage(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.SaaSDashboard())
}

func SaaSOverview(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	companies, err := repo.ListCompanies()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as empresas")
		return
	}
	plans, err := repo.ListPlans(true)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os planos")
		return
	}
	settings, err := repo.GetPlatformSettings()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar as configurações da plataforma")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"companies": companies, "plans": plans, "settings": settings}})
}

func CreateSaaSPlan(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	var input repo.CreatePlanInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if message := normalizeAndValidatePlan(&input); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	plan, err := repo.CreatePlan(input)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe um plano com este nome")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível criar o plano")
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": plan})
}

func UpdateSaaSPlan(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var input repo.UpdatePlanInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if message := normalizeAndValidatePlan(&input); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	updated, err := repo.UpdatePlan(id, input)
	if err != nil {
		if errors.Is(err, repo.ErrPlanLimitBelowUsage) {
			writeJSONError(ctx, iris.StatusConflict, "os limites informados são menores que o uso atual das empresas")
			return
		}
		if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe um plano com este nome")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o plano")
		return
	}
	if !updated {
		writeJSONError(ctx, iris.StatusNotFound, "plano não encontrado")
		return
	}
	plan, err := repo.GetPlanById(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "plano atualizado, mas não foi possível recarregá-lo")
		return
	}
	ctx.JSON(iris.Map{"data": plan})
}

func CreateSaaSCompany(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	var body struct {
		Name          string `json:"name"`
		Email         string `json:"email"`
		Timezone      string `json:"timezone"`
		PlanId        int    `json:"planId"`
		BillingCycle  string `json:"billingCycle"`
		AdminName     string `json:"adminName"`
		AdminEmail    string `json:"adminEmail"`
		AdminPassword string `json:"adminPassword"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Email = normalizeEmail(body.Email)
	body.Timezone = strings.TrimSpace(body.Timezone)
	body.BillingCycle = strings.TrimSpace(body.BillingCycle)
	body.AdminName = strings.TrimSpace(body.AdminName)
	body.AdminEmail = normalizeEmail(body.AdminEmail)
	if body.Name == "" || body.AdminName == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "nome da empresa e nome do administrador são obrigatórios")
		return
	}
	if len(body.AdminName) > 40 {
		writeJSONError(ctx, iris.StatusBadRequest, "o nome do administrador deve ter até 40 caracteres")
		return
	}
	if !validEmail(body.Email) || !validEmail(body.AdminEmail) {
		writeJSONError(ctx, iris.StatusBadRequest, "email da empresa ou do administrador inválido")
		return
	}
	if body.PlanId <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "plano inválido")
		return
	}
	if !repo.IsValidBillingCycle(body.BillingCycle) {
		writeJSONError(ctx, iris.StatusBadRequest, "ciclo de cobrança inválido")
		return
	}
	if body.Timezone != "" && !validTimezone(body.Timezone) {
		writeJSONError(ctx, iris.StatusBadRequest, "fuso horário inválido")
		return
	}
	if len(body.AdminPassword) < 8 {
		writeJSONError(ctx, iris.StatusBadRequest, "a senha do administrador deve ter pelo menos 8 caracteres")
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível proteger a senha")
		return
	}
	company, err := repo.CreateCompanyWithAdmin(repo.CreateCompanyWithAdminInput{
		Name:              body.Name,
		Email:             body.Email,
		Timezone:          body.Timezone,
		PlanId:            body.PlanId,
		BillingCycle:      body.BillingCycle,
		AdminName:         body.AdminName,
		AdminEmail:        body.AdminEmail,
		AdminPasswordHash: string(passwordHash),
	})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "plano não encontrado ou inativo")
		case errors.Is(err, repo.ErrAgentEmailInUse):
			writeJSONError(ctx, iris.StatusConflict, "o email do administrador já está em uso")
		case isUniqueViolation(err):
			writeJSONError(ctx, iris.StatusConflict, "empresa, administrador ou assinatura já cadastrada")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível criar a empresa")
		}
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": company})
}

func UpdateSaaSCompany(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var input repo.UpdateCompanyInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = normalizeEmail(input.Email)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Name == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "nome é obrigatório")
		return
	}
	if !validEmail(input.Email) {
		writeJSONError(ctx, iris.StatusBadRequest, "email inválido")
		return
	}
	if !validTimezone(input.Timezone) {
		writeJSONError(ctx, iris.StatusBadRequest, "fuso horário inválido")
		return
	}
	updated, err := repo.UpdateCompany(id, input)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe uma empresa com estes dados")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar a empresa")
		return
	}
	if !updated {
		writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
		return
	}
	company, err := repo.GetCompanyById(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "empresa atualizada, mas não foi possível recarregá-la")
		return
	}
	ctx.JSON(iris.Map{"data": company})
}

func SetSaaSCompanyStatus(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		IsActive *bool `json:"isActive"`
	}
	if err := ctx.ReadJSON(&body); err != nil || body.IsActive == nil {
		writeJSONError(ctx, iris.StatusBadRequest, "isActive é obrigatório")
		return
	}
	updated, err := repo.SetCompanyActive(id, *body.IsActive)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível alterar o status da empresa")
		return
	}
	if !updated {
		writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
		return
	}
	company, err := repo.GetCompanyById(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "status atualizado, mas não foi possível recarregar a empresa")
		return
	}
	ctx.JSON(iris.Map{"data": company})
}

func ChangeSaaSCompanyPlan(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		PlanId       int    `json:"planId"`
		BillingCycle string `json:"billingCycle"`
	}
	if err := ctx.ReadJSON(&body); err != nil || body.PlanId <= 0 || !repo.IsValidBillingCycle(body.BillingCycle) {
		writeJSONError(ctx, iris.StatusBadRequest, "plano inválido")
		return
	}
	companyPayments, err := repo.GetPaymentsByCompany(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar as cobranças da empresa")
		return
	}
	if _, err := reconcileOpenAbacatePayPayments(ctx, companyPayments); err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	subscription, err := repo.ChangeCompanyPlan(id, body.PlanId, body.BillingCycle)
	if err != nil {
		if errors.Is(err, repo.ErrOpenPayments) {
			writeJSONError(ctx, iris.StatusConflict, "cancele ou aguarde o vencimento da cobrança aberta antes de trocar o plano")
			return
		}
		if errors.Is(err, repo.ErrPlanLimitBelowUsage) {
			writeJSONError(ctx, iris.StatusConflict, "o novo plano não comporta os agentes ou conexões atuais")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "empresa, assinatura ou plano não encontrado")
			return
		}
		if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "não foi possível trocar o plano enquanto há outra assinatura ativa")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível alterar o plano da empresa")
		return
	}
	ctx.JSON(iris.Map{"data": subscription})
}

func UpdateSaaSCompanyDueDate(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		DueDate string `json:"dueDate"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
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
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(body.DueDate), location)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "data de vencimento inválida")
		return
	}
	dueDate := date.AddDate(0, 0, 1).Add(-time.Microsecond)
	companyPayments, err := repo.GetPaymentsByCompany(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar as cobranças da empresa")
		return
	}
	if _, err := reconcileOpenAbacatePayPayments(ctx, companyPayments); err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	subscription, err := repo.UpdateCurrentSubscriptionDueDate(id, dueDate)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrOpenPayments):
			writeJSONError(ctx, iris.StatusConflict, "não é possível alterar o vencimento enquanto existe uma cobrança aberta")
		case errors.Is(err, repo.ErrInvalidSubscriptionDueDate):
			writeJSONError(ctx, iris.StatusBadRequest, "o vencimento deve ser posterior ao início do período")
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "assinatura não encontrada")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o vencimento")
		}
		return
	}
	ctx.JSON(iris.Map{"data": subscription})
}

func ListSaaSCompanyPayments(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	if _, err := repo.GetCompanyById(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar a empresa")
		return
	}
	payments, err := repo.GetPaymentsByCompany(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os pagamentos")
		return
	}
	ctx.JSON(iris.Map{"data": payments})
}

func CreateSaaSCompanyCheckout(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	payment, err := createPaymentCheckout(ctx, id, "/")
	if err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	ctx.JSON(iris.Map{"data": payment})
}

func requirePlatformAdmin(ctx iris.Context) (repo.PlatformAdmin, bool) {
	admin, err := middlewares.PlatformAdminIdentifier(ctx)
	if err != nil {
		if !ctx.IsStopped() {
			writeJSONError(ctx, iris.StatusUnauthorized, "sessão da plataforma inválida")
		}
		return repo.PlatformAdmin{}, false
	}
	if !admin.IsActive {
		writeJSONError(ctx, iris.StatusForbidden, "administrador da plataforma inativo")
		return repo.PlatformAdmin{}, false
	}
	return admin, true
}

func normalizeAndValidatePlan(input *repo.PlanInput) string {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" {
		return "nome do plano é obrigatório"
	}
	if len(input.Name) > 80 {
		return "o nome do plano deve ter até 80 caracteres"
	}
	if input.MonthlyPriceCents < 0 || input.AnnualPriceCents < 0 {
		return "o preço não pode ser negativo"
	}
	if input.AgentLimit <= 0 || input.ConnectionLimit <= 0 {
		return "os limites devem ser maiores que zero"
	}
	return ""
}

func routeID(ctx iris.Context) (int, bool) {
	id, err := ctx.Params().GetInt("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "identificador inválido")
		return 0, false
	}
	return id, true
}

func validTimezone(value string) bool {
	if value == "" {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validEmail(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value)
}

func isUniqueViolation(err error) bool {
	var pqError *pq.Error
	return errors.As(err, &pqError) && pqError.Code == "23505"
}

func writeJSONError(ctx iris.Context, status int, message string) {
	ctx.StopWithJSON(status, iris.Map{"error": message})
}

func redirectRequest(ctx iris.Context, location string) {
	if strings.EqualFold(ctx.GetHeader("HX-Request"), "true") {
		ctx.Header("HX-Redirect", location)
		ctx.StatusCode(iris.StatusNoContent)
		return
	}
	ctx.Redirect(location, iris.StatusFound)
}

func securePlatformCookie(ctx iris.Context) bool {
	if ctx.Request().TLS != nil {
		return true
	}
	domain := strings.ToLower(strings.TrimSpace(env.Backend["APP_DOMAIN"]))
	return domain != "" && !strings.HasPrefix(domain, "http://")
}
