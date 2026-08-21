package controllers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sophus/internal/payments"
	"sophus/internal/repo"
	"sophus/utils/env"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

const paymentProviderRequestTimeout = 15 * time.Second

type checkoutHTTPError struct {
	status                    int
	message                   string
	checkoutCreationUncertain bool
}

func (e *checkoutHTTPError) Error() string {
	return e.message
}

func BillingPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.Billing())
}

func GetCompanyBilling(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	company, err := repo.GetCompanyById(agent.CompanyId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar a empresa")
		return
	}
	subscription, err := repo.GetCurrentSubscription(agent.CompanyId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar a assinatura")
		return
	}
	plan, err := repo.GetPlanById(subscription.PlanId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar o plano")
		return
	}
	companyPayments, err := repo.GetPaymentsByCompany(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os pagamentos")
		return
	}
	reconciled, err := reconcileOpenAbacatePayPayments(ctx, companyPayments)
	if err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	if reconciled {
		subscription, err = repo.GetCurrentSubscription(agent.CompanyId)
		if err != nil {
			writeBillingRepositoryError(ctx, err, "não foi possível atualizar a assinatura")
			return
		}
		companyPayments, err = repo.GetPaymentsByCompany(agent.CompanyId)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar os pagamentos")
			return
		}
	}
	paymentProvider, err := repo.ResolveCompanyPaymentProvider(agent.CompanyId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar o provedor de pagamento")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{
		"company":         company,
		"subscription":    subscription,
		"plan":            plan,
		"payments":        companyPayments,
		"paymentProvider": paymentProvider,
	}})
}

func CreateCompanyCheckout(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	payment, err := createPaymentCheckout(ctx, agent.CompanyId, "/billing")
	if err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	ctx.JSON(iris.Map{"data": payment})
}

func UpdateCompanyBillingCycle(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		BillingCycle string `json:"billingCycle"`
	}
	if err := ctx.ReadJSON(&body); err != nil || !repo.IsValidBillingCycle(body.BillingCycle) {
		writeJSONError(ctx, iris.StatusBadRequest, "ciclo de cobrança inválido")
		return
	}
	companyPayments, err := repo.GetPaymentsByCompany(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar as cobranças")
		return
	}
	if _, err := reconcileOpenAbacatePayPayments(ctx, companyPayments); err != nil {
		writeCheckoutError(ctx, err)
		return
	}
	current, err := repo.GetCurrentSubscription(agent.CompanyId)
	if err != nil {
		writeBillingRepositoryError(ctx, err, "não foi possível consultar a assinatura")
		return
	}
	subscription, err := repo.ChangeCompanyPlan(agent.CompanyId, current.PlanId, body.BillingCycle)
	if err != nil {
		if errors.Is(err, repo.ErrOpenPayments) {
			writeJSONError(ctx, iris.StatusConflict, "aguarde ou conclua a cobrança aberta antes de alterar o ciclo")
			return
		}
		writeBillingRepositoryError(ctx, err, "não foi possível alterar o ciclo de cobrança")
		return
	}
	ctx.JSON(iris.Map{"data": subscription})
}

func createPaymentCheckout(ctx iris.Context, companyID int, returnPath string) (repo.Payment, error) {
	company, err := repo.GetCompanyById(companyID)
	if err != nil {
		return repo.Payment{}, checkoutRepositoryError(err, "não foi possível consultar a empresa")
	}
	subscription, err := repo.GetCurrentSubscription(companyID)
	if err != nil {
		return repo.Payment{}, checkoutRepositoryError(err, "não foi possível consultar a assinatura")
	}
	plan, err := repo.GetPlanById(subscription.PlanId)
	if err != nil {
		return repo.Payment{}, checkoutRepositoryError(err, "não foi possível consultar o plano")
	}
	provider, err := repo.ResolveCompanyPaymentProvider(companyID)
	if err != nil {
		return repo.Payment{}, checkoutRepositoryError(err, "não foi possível determinar o provedor de pagamento")
	}
	if subscription.PriceCents == 0 {
		return repo.Payment{
			CompanyId:      companyID,
			SubscriptionId: subscription.Id,
			Status:         repo.PaymentStatusPaid,
			AmountCents:    0,
			DueDate:        subscription.CurrentPeriodEnd,
			Provider:       provider,
		}, nil
	}
	payment, _, err := repo.CreatePaymentForCurrentSubscription(companyID, provider)
	if err != nil {
		return repo.Payment{}, checkoutRepositoryError(err, "não foi possível preparar a cobrança")
	}
	if payment.CheckoutURL != nil && strings.TrimSpace(*payment.CheckoutURL) != "" {
		if payment.Provider != repo.PaymentProviderAbacatePay {
			return payment, nil
		}
		if err := validateCheckoutProviderConfiguration(payment.Provider); err != nil {
			return repo.Payment{}, err
		}
		payment, reusable, err := reconcileAbacatePayPayment(ctx, payment)
		if err != nil {
			return repo.Payment{}, err
		}
		if reusable {
			return payment, nil
		}
		payment, _, err = repo.CreatePaymentForCurrentSubscription(companyID, provider)
		if err != nil {
			return repo.Payment{}, checkoutRepositoryError(err, "não foi possível preparar uma nova cobrança")
		}
		if payment.CheckoutURL != nil && strings.TrimSpace(*payment.CheckoutURL) != "" {
			return payment, nil
		}
	}
	if payment.AmountCents == 0 {
		return payment, nil
	}
	if err := validateCheckoutProviderConfiguration(payment.Provider); err != nil {
		return repo.Payment{}, err
	}
	newCheckoutKey, err := randomCheckoutKey()
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível proteger o checkout"}
	}
	checkoutKey, recoverPreference, claimed, err := repo.ClaimPaymentCheckout(payment.Id, newCheckoutKey)
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível reservar a criação do checkout"}
	}
	if !claimed {
		payment, err = repo.GetPaymentByIdAndCompany(payment.Id, companyID)
		if err == nil && payment.CheckoutURL != nil && strings.TrimSpace(*payment.CheckoutURL) != "" {
			return payment, nil
		}
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusConflict, message: "checkout em preparação; tente novamente em instantes"}
	}
	checkoutSaved := false
	defer func() {
		if !checkoutSaved {
			_ = repo.ReleasePaymentCheckout(payment.Id, newCheckoutKey)
		}
	}()

	switch payment.Provider {
	case repo.PaymentProviderMercadoPago:
		payment, err = createMercadoPagoCheckout(ctx, company, plan, payment, checkoutKey, newCheckoutKey, recoverPreference, returnPath)
	case repo.PaymentProviderAbacatePay:
		payment, err = createAbacatePayCheckout(ctx, company, plan, payment, checkoutKey, newCheckoutKey, recoverPreference, returnPath)
	default:
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "provedor de pagamento inválido"}
	}
	if err != nil {
		var providerError *checkoutHTTPError
		if errors.As(err, &providerError) && providerError.checkoutCreationUncertain {
			checkoutSaved = true
		}
		return repo.Payment{}, err
	}
	checkoutSaved = true
	return payment, nil
}

func createMercadoPagoCheckout(ctx iris.Context, company repo.Company, plan repo.Plan, payment repo.Payment, checkoutKey, checkoutLease string, recoverPreference bool, returnPath string) (repo.Payment, error) {
	accessToken := strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"])
	if accessToken == "" {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "Mercado Pago não configurado"}
	}
	publicURL, err := applicationPublicURL()
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "endereço público da aplicação não configurado"}
	}
	client := payments.NewMercadoPagoClient(
		&http.Client{Timeout: paymentProviderRequestTimeout},
		accessToken,
		strings.TrimSpace(env.Backend["MERCADO_PAGO_WEBHOOK_SECRET"]),
		strings.TrimSpace(env.Backend["MERCADO_PAGO_BASE_URL"]),
	)
	if recoverPreference {
		preference, found, err := client.FindPreference(ctx.Request().Context(), strconv.Itoa(payment.Id), checkoutKey)
		if err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível recuperar o checkout no Mercado Pago", checkoutCreationUncertain: true}
		}
		if found {
			preferenceID := strings.TrimSpace(preference.ID)
			checkoutURL := strings.TrimSpace(preference.InitPoint)
			if preferenceID == "" || checkoutURL == "" {
				return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "Mercado Pago retornou um checkout recuperado inválido", checkoutCreationUncertain: true}
			}
			payment, err = repo.SetPaymentCheckout(payment.Id, checkoutLease, nil, &preferenceID, &checkoutURL)
			if err != nil {
				return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "checkout recuperado, mas não foi possível salvar a cobrança", checkoutCreationUncertain: true}
			}
			return payment, nil
		}
	}
	preference, err := client.CreatePreference(ctx.Request().Context(), payments.CreatePreferenceRequest{
		Title:             fmt.Sprintf("Plano %s - %s", plan.Name, company.Name),
		AmountCents:       payment.AmountCents,
		PayerEmail:        company.Email,
		ExternalReference: strconv.Itoa(payment.Id),
		NotificationURL:   publicURL + "/payments/mercado-pago/webhook",
		BackURLs: payments.BackURLs{
			Success: publicURL + returnPath + "?payment=success",
			Failure: publicURL + returnPath + "?payment=failure",
			Pending: publicURL + returnPath + "?payment=pending",
		},
		ExpirationDate: payment.DueDate,
		CheckoutKey:    checkoutKey,
	})
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{
			status: iris.StatusBadGateway, message: "não foi possível criar o checkout no Mercado Pago",
			checkoutCreationUncertain: !payments.IsDefinitiveHTTPFailure(err),
		}
	}
	preferenceID := strings.TrimSpace(preference.ID)
	checkoutURL := strings.TrimSpace(preference.InitPoint)
	if preferenceID == "" || checkoutURL == "" {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "Mercado Pago retornou um checkout inválido", checkoutCreationUncertain: true}
	}
	payment, err = repo.SetPaymentCheckout(payment.Id, checkoutLease, nil, &preferenceID, &checkoutURL)
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "checkout criado, mas não foi possível salvar a cobrança", checkoutCreationUncertain: true}
	}
	return payment, nil
}

func createAbacatePayCheckout(ctx iris.Context, company repo.Company, plan repo.Plan, payment repo.Payment, checkoutKey, checkoutLease string, recoverCheckout bool, returnPath string) (repo.Payment, error) {
	apiKey := strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"])
	if apiKey == "" {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "AbacatePay não configurada"}
	}
	publicURL, err := applicationPublicURL()
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "endereço público da aplicação não configurado"}
	}
	client := payments.NewAbacatePayClient(
		&http.Client{Timeout: paymentProviderRequestTimeout},
		apiKey,
		strings.TrimSpace(env.Backend["ABACATEPAY_BASE_URL"]),
	)
	externalID := strconv.Itoa(payment.Id)
	if recoverCheckout {
		checkout, found, err := findAbacatePayCheckout(ctx.Request().Context(), client, externalID, checkoutKey, 3)
		if err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível recuperar o checkout na AbacatePay", checkoutCreationUncertain: true}
		}
		if found {
			if err := validateAbacatePayCheckout(payment, checkout, checkoutKey); err != nil {
				if providerError, ok := err.(*checkoutHTTPError); ok {
					providerError.checkoutCreationUncertain = true
				}
				return repo.Payment{}, err
			}
			checkoutID := strings.TrimSpace(checkout.ID)
			checkoutURL := strings.TrimSpace(checkout.URL)
			payment, err = repo.SetPaymentCheckout(payment.Id, checkoutLease, &checkoutID, &checkoutID, &checkoutURL)
			if err != nil {
				return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "checkout recuperado, mas não foi possível salvar a cobrança", checkoutCreationUncertain: true}
			}
			return payment, nil
		}
	}

	productID, err := ensureAbacatePayProduct(ctx, client, plan, payment.AmountCents)
	if err != nil {
		return repo.Payment{}, err
	}
	checkout, err := client.CreateCheckout(
		ctx.Request().Context(),
		productID,
		externalID,
		publicURL+returnPath+"?payment=pending",
		publicURL+returnPath+"?payment=success",
		checkoutKey,
	)
	if err != nil {
		recovered, found, recoverErr := findAbacatePayCheckout(ctx.Request().Context(), client, externalID, checkoutKey, 4)
		if recoverErr == nil && found {
			checkout = recovered
		} else {
			return repo.Payment{}, &checkoutHTTPError{
				status: iris.StatusBadGateway, message: "não foi possível criar o checkout na AbacatePay",
				checkoutCreationUncertain: !payments.IsDefinitiveHTTPFailure(err),
			}
		}
	}
	if err := validateAbacatePayCheckout(payment, checkout, checkoutKey); err != nil {
		if providerError, ok := err.(*checkoutHTTPError); ok {
			providerError.checkoutCreationUncertain = true
		}
		return repo.Payment{}, err
	}
	checkoutID := strings.TrimSpace(checkout.ID)
	checkoutURL := strings.TrimSpace(checkout.URL)
	payment, err = repo.SetPaymentCheckout(payment.Id, checkoutLease, &checkoutID, &checkoutID, &checkoutURL)
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "checkout criado, mas não foi possível salvar a cobrança", checkoutCreationUncertain: true}
	}
	return payment, nil
}

func ensureAbacatePayProduct(ctx iris.Context, client *payments.AbacatePayClient, plan repo.Plan, amountCents int64) (string, error) {
	externalID := fmt.Sprintf("sophus-plan-%d-%d", plan.Id, amountCents)
	product, found, err := client.GetProductByExternalID(ctx.Request().Context(), externalID)
	if err != nil {
		return "", &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível consultar o produto na AbacatePay"}
	}
	if !found {
		description := strings.TrimSpace(plan.Description)
		if description == "" {
			description = "Acesso ao plano " + plan.Name
		}
		product, err = client.CreateProduct(ctx.Request().Context(), externalID, "Plano "+plan.Name, amountCents, description)
		if err != nil {
			// A criação concorrente pode ter concluído antes desta requisição.
			product, found, err = client.GetProductByExternalID(ctx.Request().Context(), externalID)
			if err != nil || !found {
				return "", &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível criar o produto na AbacatePay"}
			}
		}
	}
	if strings.TrimSpace(product.ID) == "" || product.ExternalID != externalID || product.Price != amountCents ||
		!strings.EqualFold(product.Currency, "BRL") || !strings.EqualFold(product.Status, "ACTIVE") || strings.TrimSpace(product.Cycle) != "" {
		return "", &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "produto da AbacatePay não confere com o plano"}
	}
	mapping, err := repo.UpsertPlanProviderProduct(repo.UpsertPlanProviderProductInput{
		PlanId:            plan.Id,
		Provider:          repo.PaymentProviderAbacatePay,
		AmountCents:       amountCents,
		ProviderProductId: product.ID,
	})
	if err != nil {
		return "", &checkoutHTTPError{status: iris.StatusInternalServerError, message: "produto criado, mas não foi possível vinculá-lo ao plano"}
	}
	return strings.TrimSpace(mapping.ProviderProductId), nil
}

func validateAbacatePayCheckout(payment repo.Payment, checkout payments.AbacateCheckout, checkoutKey string) error {
	if strings.TrimSpace(checkout.ID) == "" || strings.TrimSpace(checkout.URL) == "" {
		return &checkoutHTTPError{status: iris.StatusBadGateway, message: "AbacatePay retornou um checkout inválido"}
	}
	if checkout.ExternalID != strconv.Itoa(payment.Id) || checkout.Amount != payment.AmountCents {
		return &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "checkout da AbacatePay não confere com a cobrança"}
	}
	if subtle.ConstantTimeCompare([]byte(checkout.Metadata["checkout_key"]), []byte(checkoutKey)) != 1 {
		return &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "checkout da AbacatePay não pertence a esta cobrança"}
	}
	return nil
}

func validateCheckoutProviderConfiguration(provider string) error {
	if _, err := applicationPublicURL(); err != nil {
		return &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "endereço público da aplicação não configurado"}
	}
	switch provider {
	case repo.PaymentProviderMercadoPago:
		if strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"]) == "" {
			return &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "Mercado Pago não configurado"}
		}
	case repo.PaymentProviderAbacatePay:
		if strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]) == "" {
			return &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "AbacatePay não configurada"}
		}
	default:
		return &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "provedor de pagamento inválido"}
	}
	return nil
}

func reconcileAbacatePayPayment(ctx iris.Context, payment repo.Payment) (repo.Payment, bool, error) {
	checkoutID := ""
	if payment.ProviderPreferenceId != nil {
		checkoutID = strings.TrimSpace(*payment.ProviderPreferenceId)
	}
	if checkoutID == "" && payment.ProviderPaymentId != nil {
		checkoutID = strings.TrimSpace(*payment.ProviderPaymentId)
	}
	if checkoutID == "" || payment.CheckoutKey == nil {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "identificação do checkout da AbacatePay incompleta"}
	}
	client := payments.NewAbacatePayClient(
		&http.Client{Timeout: paymentProviderRequestTimeout},
		strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]),
		strings.TrimSpace(env.Backend["ABACATEPAY_BASE_URL"]),
	)
	checkout, err := client.GetCheckout(ctx.Request().Context(), checkoutID)
	if err != nil {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível atualizar o checkout na AbacatePay"}
	}
	if err := validateAbacatePayCheckout(payment, checkout, *payment.CheckoutKey); err != nil {
		return repo.Payment{}, false, err
	}
	mappedStatus, err := repo.MapAbacatePayPaymentStatus(checkout.Status)
	if err != nil {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusBadGateway, message: "AbacatePay retornou um status de checkout desconhecido"}
	}
	if mappedStatus == repo.PaymentStatusPending {
		return payment, true, nil
	}
	if mappedStatus == repo.PaymentStatusPaid && checkout.PaidAmount != checkout.Amount {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "valor pago não confere com a cobrança"}
	}
	var paidAt *time.Time
	if mappedStatus == repo.PaymentStatusPaid && !checkout.UpdatedAt.IsZero() {
		paidAt = &checkout.UpdatedAt
	}
	var receiptURL *string
	if value := strings.TrimSpace(checkout.ReceiptURL); value != "" {
		receiptURL = &value
	}
	updated, _, err := repo.ApplyProviderPayment(payment.Id, repo.PaymentProviderAbacatePay, checkout.ID,
		mappedStatus, paidAt, receiptURL)
	if errors.Is(err, repo.ErrProviderPaymentMismatch) {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusUnprocessableEntity, message: "checkout do provedor não confere com a cobrança"}
	}
	if err != nil {
		return repo.Payment{}, false, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível reconciliar o pagamento"}
	}
	return updated, mappedStatus == repo.PaymentStatusPaid, nil
}

func reconcileOpenAbacatePayPayments(ctx iris.Context, companyPayments []repo.Payment) (bool, error) {
	changed := false
	for _, payment := range companyPayments {
		if payment.Provider != repo.PaymentProviderAbacatePay ||
			(payment.Status != repo.PaymentStatusPending && payment.Status != repo.PaymentStatusProcessing) ||
			payment.CheckoutURL == nil || strings.TrimSpace(*payment.CheckoutURL) == "" {
			continue
		}
		if strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"]) == "" {
			return false, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "AbacatePay não configurada"}
		}
		updated, _, err := reconcileAbacatePayPayment(ctx, payment)
		if err != nil {
			return false, err
		}
		changed = changed || updated.Status != payment.Status || updated.ReceiptURL != nil && payment.ReceiptURL == nil
	}
	return changed, nil
}

func findAbacatePayCheckout(ctx context.Context, client *payments.AbacatePayClient, externalID, checkoutKey string, attempts int) (payments.AbacateCheckout, bool, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return payments.AbacateCheckout{}, false, ctx.Err()
			case <-timer.C:
			}
		}
		checkout, found, err := client.FindCheckout(ctx, externalID, checkoutKey)
		if err == nil && found {
			return checkout, true, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return payments.AbacateCheckout{}, false, lastErr
}

func MercadoPagoWebhook(ctx iris.Context) {
	var event struct {
		Type   string `json:"type"`
		Action string `json:"action"`
		Data   struct {
			ID json.RawMessage `json:"id"`
		} `json:"data"`
	}
	requestBody := http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, 1<<20)
	decoder := json.NewDecoder(requestBody)
	decodeErr := decoder.Decode(&event)
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		event = struct {
			Type   string `json:"type"`
			Action string `json:"action"`
			Data   struct {
				ID json.RawMessage `json:"id"`
			} `json:"data"`
		}{}
	}

	dataID := strings.TrimSpace(ctx.URLParam("data.id"))
	if dataID == "" {
		dataID = strings.TrimSpace(ctx.URLParam("id"))
	}
	if dataID == "" {
		dataID = rawProviderID(event.Data.ID)
	}
	if dataID == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "identificador do pagamento ausente")
		return
	}

	accessToken := strings.TrimSpace(env.Backend["MERCADO_PAGO_ACCESS_TOKEN"])
	webhookSecret := strings.TrimSpace(env.Backend["MERCADO_PAGO_WEBHOOK_SECRET"])
	if accessToken == "" || webhookSecret == "" {
		writeJSONError(ctx, iris.StatusServiceUnavailable, "webhook do Mercado Pago não configurado")
		return
	}
	client := payments.NewMercadoPagoClient(
		&http.Client{Timeout: paymentProviderRequestTimeout},
		accessToken,
		webhookSecret,
		strings.TrimSpace(env.Backend["MERCADO_PAGO_BASE_URL"]),
	)
	if !client.VerifyWebhookSignature(dataID, ctx.GetHeader("x-request-id"), ctx.GetHeader("x-signature")) {
		writeJSONError(ctx, iris.StatusUnauthorized, "assinatura do webhook inválida")
		return
	}

	eventType := strings.TrimSpace(ctx.URLParam("type"))
	if eventType == "" {
		eventType = strings.TrimSpace(ctx.URLParam("topic"))
	}
	if eventType == "" {
		eventType = event.Type
	}
	if !isMercadoPagoPaymentEvent(eventType, event.Action) {
		ctx.StatusCode(iris.StatusNoContent)
		return
	}

	providerPayment, err := client.GetPayment(ctx.Request().Context(), dataID)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadGateway, "não foi possível consultar o pagamento no Mercado Pago")
		return
	}
	if providerPayment.ID == "" || providerPayment.ID != dataID {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "identificador do pagamento não confere")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(providerPayment.CurrencyID), "BRL") {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "moeda do pagamento não confere")
		return
	}
	if _, err := repo.MapMercadoPagoPaymentStatus(providerPayment.Status); errors.Is(err, repo.ErrUnknownMercadoPagoStatus) {
		ctx.StatusCode(iris.StatusNoContent)
		return
	} else if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível interpretar o status do pagamento")
		return
	}

	internalPaymentID, err := strconv.Atoi(strings.TrimSpace(providerPayment.ExternalReference))
	if err != nil || internalPaymentID <= 0 {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "referência interna do pagamento inválida")
		return
	}
	internalPayment, err := repo.GetPaymentById(internalPaymentID)
	if errors.Is(err, sql.ErrNoRows) {
		ctx.StatusCode(iris.StatusNoContent)
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar a cobrança")
		return
	}
	if internalPayment.Provider != repo.PaymentProviderMercadoPago {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "provedor da cobrança não confere")
		return
	}
	if internalPayment.CheckoutKey == nil || subtle.ConstantTimeCompare(
		[]byte(*internalPayment.CheckoutKey), []byte(providerPayment.CheckoutKey),
	) != 1 {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "pagamento não pertence ao checkout desta cobrança")
		return
	}
	providerAmountCents, err := decimalNumberToCents(providerPayment.TransactionAmount)
	if err != nil || providerAmountCents != internalPayment.AmountCents {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "valor do pagamento não confere")
		return
	}

	_, _, err = repo.ApplyMercadoPagoPayment(
		internalPaymentID,
		providerPayment.ID,
		providerPayment.Status,
		providerPayment.DateApproved,
	)
	if errors.Is(err, repo.ErrUnknownMercadoPagoStatus) || errors.Is(err, sql.ErrNoRows) {
		ctx.StatusCode(iris.StatusNoContent)
		return
	}
	if errors.Is(err, repo.ErrProviderPaymentMismatch) {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "pagamento do provedor não confere com a cobrança")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível aplicar o pagamento")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func AbacatePayWebhook(ctx iris.Context) {
	apiKey := strings.TrimSpace(env.Backend["ABACATEPAY_API_KEY"])
	webhookSecret := strings.TrimSpace(env.Backend["ABACATEPAY_WEBHOOK_SECRET"])
	if apiKey == "" || webhookSecret == "" {
		writeJSONError(ctx, iris.StatusServiceUnavailable, "webhook da AbacatePay não configurado")
		return
	}
	if subtle.ConstantTimeCompare([]byte(ctx.URLParam("webhookSecret")), []byte(webhookSecret)) != 1 {
		writeJSONError(ctx, iris.StatusUnauthorized, "secret do webhook inválido")
		return
	}
	rawBody, err := io.ReadAll(http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, 1<<20))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "payload do webhook inválido")
		return
	}
	if !payments.VerifyAbacatePaySignature(rawBody, ctx.GetHeader("X-Webhook-Signature")) {
		writeJSONError(ctx, iris.StatusUnauthorized, "assinatura do webhook inválida")
		return
	}

	var event struct {
		ID    string          `json:"id"`
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil || strings.TrimSpace(event.ID) == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "evento do webhook inválido")
		return
	}
	event.ID = strings.TrimSpace(event.ID)
	event.Event = strings.ToLower(strings.TrimSpace(event.Event))
	if event.Event != "checkout.completed" && event.Event != "checkout.refunded" &&
		event.Event != "checkout.disputed" && event.Event != "checkout.lost" {
		ctx.StatusCode(iris.StatusOK)
		return
	}
	checkoutID := abacatePayWebhookCheckoutID(event.Data)
	if checkoutID == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "identificador do checkout ausente")
		return
	}
	client := payments.NewAbacatePayClient(
		&http.Client{Timeout: paymentProviderRequestTimeout},
		apiKey,
		strings.TrimSpace(env.Backend["ABACATEPAY_BASE_URL"]),
	)
	checkout, err := client.GetCheckout(ctx.Request().Context(), checkoutID)
	if err != nil {
		writeJSONError(ctx, iris.StatusBadGateway, "não foi possível consultar o checkout na AbacatePay")
		return
	}
	providerMappedStatus, err := repo.MapAbacatePayPaymentStatus(checkout.Status)
	if err != nil || (providerMappedStatus != repo.PaymentStatusPaid && providerMappedStatus != repo.PaymentStatusRefunded) {
		writeJSONError(ctx, iris.StatusBadGateway, "status do checkout ainda não corresponde ao evento")
		return
	}
	if event.Event == "checkout.refunded" && providerMappedStatus != repo.PaymentStatusRefunded {
		writeJSONError(ctx, iris.StatusBadGateway, "status do checkout ainda não corresponde ao evento")
		return
	}

	internalPaymentID, err := strconv.Atoi(strings.TrimSpace(checkout.ExternalID))
	if err != nil || internalPaymentID <= 0 {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "referência interna do checkout inválida")
		return
	}
	internalPayment, err := repo.GetPaymentById(internalPaymentID)
	if errors.Is(err, sql.ErrNoRows) {
		ctx.StatusCode(iris.StatusOK)
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar a cobrança")
		return
	}
	if internalPayment.Provider != repo.PaymentProviderAbacatePay || checkout.Amount != internalPayment.AmountCents {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "checkout não confere com a cobrança")
		return
	}
	if internalPayment.ProviderPreferenceId != nil && *internalPayment.ProviderPreferenceId != checkout.ID {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "identificador do checkout não confere")
		return
	}
	if internalPayment.CheckoutKey == nil || subtle.ConstantTimeCompare(
		[]byte(*internalPayment.CheckoutKey), []byte(checkout.Metadata["checkout_key"]),
	) != 1 {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "checkout não pertence a esta cobrança")
		return
	}
	if strings.EqualFold(checkout.Status, "PAID") && checkout.PaidAmount != checkout.Amount {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "valor pago não confere com a cobrança")
		return
	}
	mappedStatus := providerMappedStatus
	if event.Event == "checkout.disputed" && providerMappedStatus != repo.PaymentStatusRefunded {
		mappedStatus = internalPayment.Status
	} else if event.Event == "checkout.lost" {
		mappedStatus = repo.PaymentStatusRefunded
	}
	var paidAt *time.Time
	if providerMappedStatus == repo.PaymentStatusPaid && !checkout.UpdatedAt.IsZero() {
		paidAt = &checkout.UpdatedAt
	}
	var receiptURL *string
	if value := strings.TrimSpace(checkout.ReceiptURL); value != "" {
		receiptURL = &value
	}
	_, _, err = repo.ApplyProviderPaymentEvent(event.ID, internalPaymentID, repo.PaymentProviderAbacatePay,
		checkout.ID, mappedStatus, paidAt, receiptURL)
	if errors.Is(err, repo.ErrProviderPaymentMismatch) {
		writeJSONError(ctx, iris.StatusUnprocessableEntity, "checkout do provedor não confere com a cobrança")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		ctx.StatusCode(iris.StatusOK)
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível aplicar o pagamento")
		return
	}
	ctx.StatusCode(iris.StatusOK)
}

func abacatePayWebhookCheckoutID(raw json.RawMessage) string {
	var data struct {
		ID      string `json:"id"`
		Billing *struct {
			ID string `json:"id"`
		} `json:"billing"`
		Checkout *struct {
			ID string `json:"id"`
		} `json:"checkout"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return ""
	}
	if data.Billing != nil && strings.TrimSpace(data.Billing.ID) != "" {
		return strings.TrimSpace(data.Billing.ID)
	}
	if data.Checkout != nil && strings.TrimSpace(data.Checkout.ID) != "" {
		return strings.TrimSpace(data.Checkout.ID)
	}
	return strings.TrimSpace(data.ID)
}

func decimalNumberToCents(number json.Number) (int64, error) {
	value := strings.TrimSpace(number.String())
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") || strings.Count(value, ".") > 1 {
		return 0, errors.New("invalid decimal amount")
	}
	parts := strings.SplitN(value, ".", 2)
	if parts[0] == "" || !decimalDigits(parts[0]) {
		return 0, errors.New("invalid decimal amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 2 || !decimalDigits(fraction) {
			return 0, errors.New("invalid decimal amount")
		}
	}
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("invalid decimal amount")
	}
	var fractionalCents uint64
	if fraction != "" {
		fractionalCents, err = strconv.ParseUint(fraction, 10, 8)
		if err != nil {
			return 0, errors.New("invalid decimal amount")
		}
		if len(fraction) == 1 {
			fractionalCents *= 10
		}
	}
	if whole > math.MaxInt64/100 || (whole == math.MaxInt64/100 && fractionalCents > math.MaxInt64%100) {
		return 0, errors.New("decimal amount overflows cents")
	}
	return int64(whole*100 + fractionalCents), nil
}

func randomCheckoutKey() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func decimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func rawProviderID(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return ""
		}
		return strings.TrimSpace(value)
	}
	value := string(raw)
	if !decimalDigits(value) {
		return ""
	}
	return value
}

func isMercadoPagoPaymentEvent(eventType, action string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	action = strings.ToLower(strings.TrimSpace(action))
	if eventType != "" && eventType != "payment" {
		return false
	}
	return action == "" || strings.HasPrefix(action, "payment.")
}

func checkoutRepositoryError(err error, fallback string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return &checkoutHTTPError{status: iris.StatusNotFound, message: "empresa, assinatura ou plano não encontrado"}
	}
	return &checkoutHTTPError{status: iris.StatusInternalServerError, message: fallback}
}

func writeCheckoutError(ctx iris.Context, err error) {
	var controllerError *checkoutHTTPError
	if errors.As(err, &controllerError) {
		writeJSONError(ctx, controllerError.status, controllerError.message)
		return
	}
	writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível preparar o checkout")
}

func writeBillingRepositoryError(ctx iris.Context, err error, fallback string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "empresa, assinatura ou plano não encontrado")
		return
	}
	writeJSONError(ctx, iris.StatusInternalServerError, fallback)
}
