package controllers

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"sophus/internal/payments"
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
	addonPrices, err := repo.GetAddonPrices()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar os preços dos adicionais")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"companies": companies, "plans": plans, "settings": settings, "addonPrices": addonPrices}})
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
		if errors.Is(err, repo.ErrAddonsNotAllowed) {
			writeJSONError(ctx, 409, "este plano possui empresas com adicionais contratados; remova os adicionais antes de desabilitar a contratação")
			return
		}
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
		case errors.Is(err, repo.ErrInvalidTrialDays):
			writeJSONError(ctx, iris.StatusBadRequest, "o período de teste deve ser menor que o ciclo contratado")
		case isUniqueViolation(err):
			writeJSONError(ctx, iris.StatusConflict, "o email da empresa ou do administrador já está cadastrado")
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
			writeJSONError(ctx, iris.StatusConflict, "este email já está cadastrado por outra empresa")
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
	provider, err := repo.ResolveCompanyPaymentProvider(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível determinar o provedor de pagamento")
		return
	}
	payment, err := repo.RequestCompanyPlanChange(id, body.PlanId, body.BillingCycle, provider)
	if err != nil {
		if errors.Is(err, repo.ErrOpenPayments) {
			writeJSONError(ctx, iris.StatusConflict, "cancele ou aguarde o vencimento da cobrança aberta antes de trocar o plano")
			return
		}
		if errors.Is(err, repo.ErrPlanLimitBelowUsage) {
			writeJSONError(ctx, iris.StatusConflict, "o novo plano não comporta os agentes ou conexões atuais")
			return
		}
		if errors.Is(err, repo.ErrNoSubscriptionChange) {
			writeJSONError(ctx, iris.StatusConflict, "este plano e ciclo já estão contratados")
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
	if payment.Status != repo.PaymentStatusPaid {
		payment, err = createPaymentCheckout(ctx, id, "/")
		if err != nil {
			writeCheckoutError(ctx, err)
			return
		}
	}
	ctx.JSON(iris.Map{"data": payment})
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

func ReactivateSaaSCompanySubscription(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		Days   int    `json:"days"`
		Reason string `json:"reason"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	subscription, err := repo.ReactivateCompanySubscription(id, admin.Id, body.Days, strings.TrimSpace(body.Reason))
	if err != nil {
		writeSubscriptionAdjustmentError(ctx, err, "não foi possível reativar a assinatura")
		return
	}
	ctx.JSON(iris.Map{"data": subscription})
}

func GrantSaaSCompanySubscriptionRedemption(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		Days   int    `json:"days"`
		Reason string `json:"reason"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	subscription, redemptionEndsAt, err := repo.GrantCompanySubscriptionRedemption(id, admin.Id, body.Days, strings.TrimSpace(body.Reason))
	if err != nil {
		writeSubscriptionAdjustmentError(ctx, err, "não foi possível conceder a janela de resgate")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"subscription": subscription, "redemptionEndsAt": redemptionEndsAt}})
}

func writeSubscriptionAdjustmentError(ctx iris.Context, err error, fallback string) {
	switch {
	case errors.Is(err, repo.ErrInvalidSubscriptionAdjustment):
		writeJSONError(ctx, iris.StatusBadRequest, "informe uma quantidade válida de dias e uma justificativa de 3 a 500 caracteres")
	case errors.Is(err, repo.ErrOpenPayments):
		writeJSONError(ctx, iris.StatusConflict, "existe uma cobrança aberta para esta assinatura")
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, iris.StatusNotFound, "assinatura não encontrada")
	case isUniqueViolation(err):
		writeJSONError(ctx, iris.StatusConflict, "já existe outra assinatura ativa para esta empresa")
	default:
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
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

func ListSaaSPayments(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	payments, err := repo.GetPayments()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as cobranças")
		return
	}
	companies, err := repo.ListCompanies()
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar as empresas")
		return
	}
	companyNames := make(map[int]string, len(companies))
	for _, company := range companies {
		companyNames[company.Id] = company.Name
	}
	result := make([]iris.Map, 0, len(payments))
	for _, payment := range payments {
		result = append(result, iris.Map{"payment": payment, "companyName": companyNames[payment.CompanyId]})
	}
	ctx.JSON(iris.Map{"data": result})
}

func UpdateSaaSCompanyPayment(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	companyID, ok := routeID(ctx)
	if !ok {
		return
	}
	paymentID, err := ctx.Params().GetInt("paymentId")
	if err != nil || paymentID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "cobrança inválida")
		return
	}
	var body struct {
		Action      string `json:"action"`
		DueDate     string `json:"dueDate"`
		AmountCents *int64 `json:"amountCents"`
		Reason      string `json:"reason"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	company, err := repo.GetCompanyById(companyID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "empresa não encontrada")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar a empresa")
		return
	}
	current, err := repo.GetPaymentByIdAndCompany(paymentID, companyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "cobrança não encontrada")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar a cobrança")
		}
		return
	}
	if !canManagePendingCompanyPayment(current) {
		writeJSONError(ctx, iris.StatusConflict, "apenas cobranças pendentes e sem pagamento registrado podem ser alteradas")
		return
	}
	issued := current.CheckoutURL != nil && strings.TrimSpace(*current.CheckoutURL) != ""
	if body.Action == "confirm_paid" {
		reason := strings.TrimSpace(body.Reason)
		if reason == "" || len(reason) > 500 {
			writeJSONError(ctx, iris.StatusBadRequest, "informe o motivo da confirmação (até 500 caracteres)")
			return
		}
		if issued {
			switch current.Provider {
			case repo.PaymentProviderAbacatePay:
				if strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]) == "" {
					writeJSONError(ctx, iris.StatusServiceUnavailable, "PIX não configurado")
					return
				}
				verified, pending, verifyErr := reconcileAbacatePayPayment(ctx, current)
				if verifyErr != nil {
					writeCheckoutError(ctx, verifyErr)
					return
				}
				if !pending || verified.Status != repo.PaymentStatusPending {
					writeJSONError(ctx, iris.StatusConflict, "o checkout já mudou de status no provedor; atualize a fatura")
					return
				}
			case repo.PaymentProviderMercadoPago:
				if current.ProviderPreferenceId == nil || current.CheckoutKey == nil {
					writeJSONError(ctx, iris.StatusConflict, "checkout sem identificação verificável")
					return
				}
				if strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"]) == "" {
					writeJSONError(ctx, iris.StatusServiceUnavailable, "Mercado Pago não configurado")
					return
				}
				client := payments.NewMercadoPagoClient(&http.Client{Timeout: paymentProviderRequestTimeout}, env.Backend["MERCADO_PAGO_ACCESS_TOKEN"], env.Backend["MERCADO_PAGO_WEBHOOK_SECRET"], env.Backend["MERCADO_PAGO_BASE_URL"])
				if err := client.ExpirePreference(ctx.Request().Context(), *current.ProviderPreferenceId, strconv.Itoa(paymentID), *current.CheckoutKey); err != nil {
					writeJSONError(ctx, iris.StatusBadGateway, "não foi possível desativar o link no Mercado Pago")
					return
				}
			default:
				writeJSONError(ctx, iris.StatusBadRequest, "provedor da cobrança inválido")
				return
			}
		} else if current.CheckoutKey != nil && strings.TrimSpace(*current.CheckoutKey) != "" {
			if err := clearUnlinkedInvoiceCheckout(ctx, current); err != nil {
				writeCheckoutError(ctx, err)
				return
			}
		} else if err := repo.ReleaseStaleUnlinkedPaymentCheckout(paymentID, companyID); err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível liberar o checkout anterior")
			return
		}
		payment, confirmErr := repo.ConfirmPendingPaymentManually(paymentID, companyID, admin.Id, reason)
		if confirmErr != nil {
			if errors.Is(confirmErr, sql.ErrNoRows) {
				writeJSONError(ctx, iris.StatusConflict, "a fatura mudou de estado; atualize a página")
			} else {
				log.Printf("manual payment confirmation failed: payment=%d error=%v", paymentID, confirmErr)
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível confirmar o pagamento")
			}
			return
		}
		if auditErr := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &companyID, ActorID: &admin.Id, ActorEmail: admin.Email,
			ActorType: "platform", Action: "confirm_paid", TargetType: "payment", TargetID: strconv.Itoa(paymentID), IPAddress: ctx.RemoteAddr()}); auditErr != nil {
			log.Printf("manual payment audit failed: payment=%d: %v", paymentID, auditErr)
		}
		ctx.JSON(iris.Map{"data": payment})
		return
	}
	if body.Action == "discard" {
		if !issued || current.Provider != repo.PaymentProviderAbacatePay {
			writeJSONError(ctx, iris.StatusBadRequest, "descarte disponível apenas para checkout PIX emitido")
			return
		}
		if strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]) == "" {
			writeJSONError(ctx, iris.StatusServiceUnavailable, "PIX não configurado")
			return
		}
		verified, pending, reconcileErr := reconcileAbacatePayPayment(ctx, current)
		if reconcileErr != nil {
			writeCheckoutError(ctx, reconcileErr)
			return
		}
		if !pending || verified.Status != repo.PaymentStatusPending || verified.ProviderPreferenceId == nil || verified.CheckoutKey == nil {
			writeJSONError(ctx, iris.StatusConflict, "a cobrança já mudou de status no provedor; atualize a página")
			return
		}
		payment, discardErr := repo.DiscardPendingAbacatePayPayment(paymentID, companyID, *verified.ProviderPreferenceId, *verified.CheckoutKey)
		if discardErr != nil {
			if errors.Is(discardErr, sql.ErrNoRows) {
				writeJSONError(ctx, iris.StatusConflict, "a cobrança mudou de estado; atualize a página")
			} else {
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível descartar a cobrança")
			}
			return
		}
		if auditErr := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &companyID, ActorID: &admin.Id,
			ActorEmail: admin.Email, ActorType: "platform", Action: "discard", TargetType: "payment", TargetID: strconv.Itoa(paymentID), IPAddress: ctx.RemoteAddr()}); auditErr != nil {
			log.Printf("payment discard audit failed: payment=%d: %v", paymentID, auditErr)
		}
		ctx.JSON(iris.Map{"data": payment})
		return
	}
	if issued && current.Provider != repo.PaymentProviderMercadoPago {
		writeJSONError(ctx, iris.StatusConflict, "o provedor PIX não permite alterar ou cancelar um checkout emitido; este link ainda pode ser pago")
		return
	}
	if !issued && current.CheckoutKey != nil && strings.TrimSpace(*current.CheckoutKey) != "" {
		if err := clearUnlinkedInvoiceCheckout(ctx, current); err != nil {
			writeCheckoutError(ctx, err)
			return
		}
		current.CheckoutKey = nil
	}
	if !issued && current.CheckoutKey == nil {
		if err := repo.ReleaseStaleUnlinkedPaymentCheckout(paymentID, companyID); err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível liberar o checkout anterior")
			return
		}
	}
	var preferenceID, checkoutKey string
	var client *payments.MercadoPagoClient
	if issued {
		if current.ProviderPreferenceId == nil || current.CheckoutKey == nil || strings.TrimSpace(*current.ProviderPreferenceId) == "" || strings.TrimSpace(*current.CheckoutKey) == "" {
			writeJSONError(ctx, iris.StatusConflict, "checkout sem identificação verificável no provedor")
			return
		}
		preferenceID, checkoutKey = *current.ProviderPreferenceId, *current.CheckoutKey
		if strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"]) == "" {
			writeJSONError(ctx, iris.StatusServiceUnavailable, "Mercado Pago não configurado")
			return
		}
		client = payments.NewMercadoPagoClient(&http.Client{Timeout: paymentProviderRequestTimeout},
			env.Backend["MERCADO_PAGO_ACCESS_TOKEN"], env.Backend["MERCADO_PAGO_WEBHOOK_SECRET"], env.Backend["MERCADO_PAGO_BASE_URL"])
	}
	var payment repo.Payment
	providerUpdated := false
	switch strings.TrimSpace(body.Action) {
	case "update_due_date", "update":
		location, locationErr := time.LoadLocation(company.Timezone)
		if locationErr != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "fuso horário da empresa inválido")
			return
		}
		date, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(body.DueDate), location)
		if parseErr != nil {
			writeJSONError(ctx, iris.StatusBadRequest, "data de vencimento inválida")
			return
		}
		dueDate := date.AddDate(0, 0, 1).Add(-time.Microsecond)
		if !dueDate.After(time.Now()) {
			writeJSONError(ctx, iris.StatusBadRequest, "escolha um vencimento futuro")
			return
		}
		amount := current.AmountCents
		if body.AmountCents != nil {
			amount = *body.AmountCents
		}
		if amount < 1 || amount > 1000000000 {
			writeJSONError(ctx, iris.StatusBadRequest, "informe um valor entre R$ 0,01 e R$ 10.000.000,00")
			return
		}
		if issued {
			planID, planErr := repo.GetPaymentPlanID(paymentID)
			if planErr != nil {
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar o plano da cobrança")
				return
			}
			plan, planErr := repo.GetPlanById(planID)
			if planErr != nil {
				writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar o plano")
				return
			}
			if err = client.UpdatePreferenceAmount(ctx.Request().Context(), preferenceID, strconv.Itoa(paymentID), checkoutKey,
				fmt.Sprintf("Plano %s - %s", plan.Name, company.Name), amount, dueDate); err != nil {
				log.Printf("payment preference update failed: payment=%d: %v", paymentID, err)
				writeJSONError(ctx, iris.StatusBadGateway, "não foi possível confirmar a atualização do link no Mercado Pago; atualize a fatura antes de tentar novamente")
				return
			}
			providerUpdated = true
		}
		payment, err = repo.UpdatePendingPaymentDetails(paymentID, companyID, amount, dueDate, preferenceID)
	case "cancel":
		if issued {
			if err = client.ExpirePreference(ctx.Request().Context(), preferenceID, strconv.Itoa(paymentID), checkoutKey); err != nil {
				log.Printf("payment preference expiry failed: payment=%d: %v", paymentID, err)
				writeJSONError(ctx, iris.StatusBadGateway, "não foi possível desativar o link no Mercado Pago; a fatura continua pendente")
				return
			}
			providerUpdated = true
			payment, err = repo.CancelPendingMercadoPagoPayment(paymentID, companyID, preferenceID)
		} else {
			payment, err = repo.CancelPendingPayment(paymentID, companyID)
		}
	default:
		writeJSONError(ctx, iris.StatusBadRequest, "ação de cobrança inválida")
		return
	}
	if err != nil {
		if providerUpdated {
			log.Printf("payment provider updated but database update failed: company=%d payment=%d action=%s: %v", companyID, paymentID, body.Action, err)
			writeJSONError(ctx, iris.StatusConflict, "o link foi alterado no provedor, mas a fatura não pôde ser salva; contate o suporte antes de reutilizá-la")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusConflict, "a cobrança mudou de estado; atualize a página e tente novamente")
			return
		}
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar a cobrança")
		return
	}
	if auditErr := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &companyID, ActorID: &admin.Id,
		ActorEmail: admin.Email, ActorType: "platform", Action: body.Action, TargetType: "payment", TargetID: strconv.Itoa(paymentID), IPAddress: ctx.RemoteAddr()}); auditErr != nil {
		log.Printf("payment audit failed: payment=%d: %v", paymentID, auditErr)
	}
	ctx.JSON(iris.Map{"data": payment})
}

func canManagePendingCompanyPayment(payment repo.Payment) bool {
	if payment.Status != repo.PaymentStatusPending {
		return false
	}
	if payment.ProviderPaymentId == nil {
		return true
	}
	return payment.Provider == repo.PaymentProviderAbacatePay && payment.ProviderPreferenceId != nil &&
		*payment.ProviderPaymentId == *payment.ProviderPreferenceId
}

func GetSaaSCompanyCredits(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	credits, err := repo.GetCompanyCreditSummary(id)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar os créditos")
		return
	}
	ctx.JSON(iris.Map{"data": credits})
}

func clearUnlinkedInvoiceCheckout(ctx iris.Context, payment repo.Payment) error {
	if payment.CheckoutKey == nil || payment.CheckoutURL != nil || payment.ProviderPreferenceId != nil || payment.ProviderPaymentId != nil {
		return &checkoutHTTPError{status: iris.StatusConflict, message: "checkout em preparação; atualize a fatura"}
	}
	if time.Since(payment.UpdatedAt) < 5*time.Minute {
		return &checkoutHTTPError{status: iris.StatusConflict, message: "checkout em preparação; tente novamente em alguns minutos"}
	}
	key := *payment.CheckoutKey
	switch payment.Provider {
	case repo.PaymentProviderMercadoPago:
		if strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"]) == "" {
			return &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "Mercado Pago não configurado"}
		}
		client := payments.NewMercadoPagoClient(&http.Client{Timeout: paymentProviderRequestTimeout}, env.Backend["MERCADO_PAGO_ACCESS_TOKEN"], env.Backend["MERCADO_PAGO_WEBHOOK_SECRET"], env.Backend["MERCADO_PAGO_BASE_URL"])
		preference, found, err := client.FindPreference(ctx.Request().Context(), strconv.Itoa(payment.Id), key)
		if err != nil {
			return &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível verificar o link antigo no Mercado Pago"}
		}
		if found {
			if err := client.ExpirePreference(ctx.Request().Context(), preference.ID, strconv.Itoa(payment.Id), key); err != nil {
				return &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível desativar o link antigo no Mercado Pago"}
			}
		}
	case repo.PaymentProviderAbacatePay:
		if strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]) == "" {
			return &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "PIX não configurado"}
		}
		client := payments.NewAbacatePayClient(&http.Client{Timeout: paymentProviderRequestTimeout}, env.Backend["ABACATEPAY_API_KEY"], env.Backend["ABACATEPAY_BASE_URL"])
		_, found, err := client.FindCheckout(ctx.Request().Context(), strconv.Itoa(payment.Id), key)
		if err != nil {
			return &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível verificar o checkout PIX antigo"}
		}
		if found {
			return &checkoutHTTPError{status: iris.StatusConflict, message: "o checkout PIX antigo ainda pode ser pago e não pode ser cancelado pelo provedor"}
		}
	default:
		return &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "provedor de pagamento inválido"}
	}
	if err := repo.ClearUnlinkedPaymentCheckout(payment.Id, payment.CompanyId, key); err != nil {
		return checkoutRepositoryError(err, "não foi possível liberar o checkout antigo")
	}
	return nil
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
	if input.TrialDays < 0 || input.TrialDays > 90 {
		return "o período de avaliação deve ter entre 0 e 90 dias"
	}
	if input.MonthlyPriceCents > 0 && input.TrialDays >= 30 {
		return "o período de avaliação deve ser menor que os 30 dias do ciclo mensal"
	}
	if input.StorageLimitGB < 1 || input.StorageLimitGB > 100000 {
		return "o armazenamento deve ter entre 1 e 100000 GB"
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
