package controllers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sophus/internal/payments"
	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
)

const asaasMaxLinkDescriptionLength = 150

// Repository calls used by the Asaas checkout and webhook, replaceable in tests.
var (
	asaasGetPayment         = repo.GetPaymentByAsaasLink
	asaasListPaymentLinks   = repo.ListAsaasPaymentLinks
	asaasRecordPaymentLink  = repo.RecordAsaasPaymentLink
	asaasSwitchCheckout     = repo.SwitchAsaasCheckout
	asaasApplyPaymentEvent  = repo.ApplyProviderPaymentEvent
	asaasRecordSurplusEvent = repo.RecordSurplusCheckoutEvent
)

func asaasConfigured() bool {
	return strings.TrimSpace(env.Backend["ASAAS_API_KEY"]) != ""
}

func newAsaasClient(timeout time.Duration) *payments.AsaasClient {
	return payments.NewAsaasClient(
		&http.Client{Timeout: timeout},
		strings.TrimSpace(env.Backend["ASAAS_API_KEY"]),
		asaasBaseURL(),
	)
}

// asaasBaseURL selects the API environment. ASAAS_SANDBOX=true switches to the
// sandbox; an explicit ASAAS_BASE_URL still wins, for proxies and tests.
// An empty result makes the client use production.
func asaasBaseURL() string {
	if baseURL := strings.TrimSpace(env.Backend["ASAAS_BASE_URL"]); baseURL != "" {
		return baseURL
	}
	if sandbox, err := strconv.ParseBool(strings.TrimSpace(env.Backend["ASAAS_SANDBOX"])); err == nil && sandbox {
		return payments.AsaasSandboxBaseURL
	}
	return ""
}

func createAsaasCheckout(ctx iris.Context, company repo.Company, plan repo.Plan, payment repo.Payment, checkoutKey, checkoutLease string, recoverLink bool, billingType string) (repo.Payment, error) {
	if !asaasConfigured() {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "Asaas não configurado"}
	}
	billingType, valid := payments.NormalizeAsaasBillingType(billingType)
	if !valid {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadRequest, message: "forma de pagamento inválida"}
	}
	client := newAsaasClient(paymentProviderRequestTimeout)
	if recoverLink {
		links, err := client.FindPaymentLinks(ctx.Request().Context(), payment.Id, checkoutKey)
		if err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível recuperar o link de pagamento no Asaas", checkoutCreationUncertain: true}
		}
		var recovered *payments.AsaasPaymentLink
		for index := range links {
			if err := recordAsaasLink(payment.Id, checkoutKey, links[index]); err != nil {
				return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível registrar o link de pagamento recuperado", checkoutCreationUncertain: true}
			}
			if recovered == nil && asaasLinkBillingType(links[index]) == billingType {
				recovered = &links[index]
			}
		}
		if recovered != nil {
			return saveAsaasCheckout(payment, *recovered, checkoutLease, "checkout recuperado, mas não foi possível salvar a cobrança")
		}
	}
	link, err := createAsaasLink(ctx, client, company, plan, payment, checkoutKey, billingType)
	if err != nil {
		return repo.Payment{}, err
	}
	if err := recordAsaasLink(payment.Id, checkoutKey, link); err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "checkout criado, mas não foi possível registrar o link", checkoutCreationUncertain: true}
	}
	return saveAsaasCheckout(payment, link, checkoutLease, "checkout criado, mas não foi possível salvar a cobrança")
}

func createAsaasLink(ctx iris.Context, client *payments.AsaasClient, company repo.Company, plan repo.Plan, payment repo.Payment, checkoutKey, billingType string) (payments.AsaasPaymentLink, error) {
	link, err := client.CreatePaymentLink(ctx.Request().Context(), payments.CreateAsaasPaymentLinkRequest{
		Name:              payments.AsaasLinkName(payment.Id, plan.Name),
		Description:       truncateRunes(fmt.Sprintf("Plano %s - %s", plan.Name, company.Name), asaasMaxLinkDescriptionLength),
		AmountCents:       payment.AmountCents,
		EndDate:           paymentCheckoutExpiration(payment.DueDate, time.Now().UTC()),
		ExternalReference: payments.AsaasLinkReference(payment.Id, checkoutKey),
		BillingType:       billingType,
	})
	if err != nil {
		log.Printf("Asaas payment link creation failed: payment=%d error=%v", payment.Id, err)
		return payments.AsaasPaymentLink{}, &checkoutHTTPError{
			status: iris.StatusBadGateway, message: "não foi possível criar o link de pagamento no Asaas",
			checkoutCreationUncertain: !payments.IsDefinitiveHTTPFailure(err),
		}
	}
	if cents, err := payments.AsaasAmountCents(link.Value); err != nil || cents != payment.AmountCents {
		return payments.AsaasPaymentLink{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "o Asaas retornou um link com valor diferente da cobrança", checkoutCreationUncertain: true}
	}
	if strings.TrimSpace(link.ID) == "" || strings.TrimSpace(link.URL) == "" {
		return payments.AsaasPaymentLink{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "o Asaas retornou um link de pagamento inválido", checkoutCreationUncertain: true}
	}
	return link, nil
}

func asaasLinkBillingType(link payments.AsaasPaymentLink) string {
	billingType, ok := payments.NormalizeAsaasBillingType(link.BillingType)
	if !ok {
		return payments.AsaasBillingTypeUndefined
	}
	return billingType
}

func recordAsaasLink(paymentID int, checkoutKey string, link payments.AsaasPaymentLink) error {
	return asaasRecordPaymentLink(repo.AsaasPaymentLink{
		LinkID: strings.TrimSpace(link.ID), PaymentID: paymentID, BillingType: asaasLinkBillingType(link),
		CheckoutKey: checkoutKey, CheckoutURL: strings.TrimSpace(link.URL),
	})
}

func saveAsaasCheckout(payment repo.Payment, link payments.AsaasPaymentLink, checkoutLease, saveFailure string) (repo.Payment, error) {
	linkID := strings.TrimSpace(link.ID)
	checkoutURL := strings.TrimSpace(link.URL)
	if linkID == "" || checkoutURL == "" {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "o Asaas retornou um link de pagamento inválido", checkoutCreationUncertain: true}
	}
	payment, err := repo.SetPaymentCheckout(payment.Id, checkoutLease, nil, &linkID, &checkoutURL)
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: saveFailure, checkoutCreationUncertain: true}
	}
	return payment, nil
}

// switchAsaasCheckoutMethod points an issued invoice to a link for the payment
// method the payer picked, reusing the link already issued for that method.
// Links for other methods stay mapped, so charges generated through them still
// settle the invoice (or become credit when it is already paid).
func switchAsaasCheckoutMethod(ctx iris.Context, company repo.Company, plan repo.Plan, payment repo.Payment, billingType string) (repo.Payment, error) {
	if payment.Status != repo.PaymentStatusPending || payment.ProviderPaymentId != nil ||
		payment.CheckoutKey == nil || payment.ProviderPreferenceId == nil || payment.CheckoutURL == nil {
		return payment, nil
	}
	if !asaasConfigured() {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusServiceUnavailable, message: "Asaas não configurado"}
	}
	checkoutKey, currentLinkID := *payment.CheckoutKey, *payment.ProviderPreferenceId
	links, err := asaasListPaymentLinks(payment.Id)
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível consultar os links de pagamento"}
	}
	client := newAsaasClient(paymentProviderRequestTimeout)
	currentMapped := false
	var target *repo.AsaasPaymentLink
	for index := range links {
		if links[index].LinkID == currentLinkID {
			currentMapped = true
			if links[index].BillingType == billingType {
				return payment, nil
			}
		}
		if target == nil && links[index].BillingType == billingType && links[index].CheckoutKey == checkoutKey {
			target = &links[index]
		}
	}
	if !currentMapped {
		// A link issued before methods were selectable: map it before the
		// invoice stops pointing to it.
		current, err := client.GetPaymentLink(ctx.Request().Context(), currentLinkID)
		if err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusBadGateway, message: "não foi possível consultar o link atual no Asaas"}
		}
		if current.ExternalReference != payments.AsaasLinkReference(payment.Id, checkoutKey) {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusConflict, message: "o link atual não pertence a esta cobrança; contate o suporte"}
		}
		if err := recordAsaasLink(payment.Id, checkoutKey, current); err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível registrar o link atual"}
		}
		if asaasLinkBillingType(current) == billingType {
			return payment, nil
		}
	}
	if target == nil {
		link, err := createAsaasLink(ctx, client, company, plan, payment, checkoutKey, billingType)
		if err != nil {
			return repo.Payment{}, err
		}
		if err := recordAsaasLink(payment.Id, checkoutKey, link); err != nil {
			return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "link criado, mas não foi possível registrá-lo; tente novamente"}
		}
		target = &repo.AsaasPaymentLink{LinkID: strings.TrimSpace(link.ID), CheckoutURL: strings.TrimSpace(link.URL)}
	}
	updated, err := asaasSwitchCheckout(payment.Id, payment.CompanyId, checkoutKey, target.LinkID, target.CheckoutURL)
	if errors.Is(err, sql.ErrNoRows) {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusConflict, message: "a cobrança mudou de estado; atualize a página"}
	}
	if err != nil {
		return repo.Payment{}, &checkoutHTTPError{status: iris.StatusInternalServerError, message: "não foi possível trocar a forma de pagamento"}
	}
	return updated, nil
}

type asaasInvoiceLink struct {
	id        string
	reference string
}

// asaasInvoiceLinks lists the links issued under the invoice's current checkout
// key, including the one it points to when that link predates the mapping.
// Links of a cleared checkout key were already deleted at Asaas.
func asaasInvoiceLinks(payment repo.Payment) ([]asaasInvoiceLink, error) {
	if payment.CheckoutKey == nil {
		return nil, nil
	}
	mapped, err := asaasListPaymentLinks(payment.Id)
	if err != nil {
		return nil, err
	}
	links := make([]asaasInvoiceLink, 0, len(mapped)+1)
	seen := map[string]bool{}
	for _, link := range mapped {
		if link.CheckoutKey != *payment.CheckoutKey {
			continue
		}
		seen[link.LinkID] = true
		links = append(links, asaasInvoiceLink{id: link.LinkID, reference: payments.AsaasLinkReference(payment.Id, link.CheckoutKey)})
	}
	if payment.ProviderPreferenceId != nil && !seen[*payment.ProviderPreferenceId] {
		links = append(links, asaasInvoiceLink{id: *payment.ProviderPreferenceId, reference: payments.AsaasLinkReference(payment.Id, *payment.CheckoutKey)})
	}
	return links, nil
}

// asaasIssuedLink lets platform admins update or cancel the Asaas links of an
// invoice through the same flow used for Mercado Pago preferences.
type asaasIssuedLink struct {
	client *payments.AsaasClient
	links  []asaasInvoiceLink
}

func (l asaasIssuedLink) Update(ctx context.Context, _ string, amountCents int64, dueDate time.Time) error {
	for _, link := range l.links {
		if err := l.client.UpdatePaymentLink(ctx, link.id, link.reference, amountCents, dueDate); err != nil {
			return err
		}
	}
	return nil
}

func (l asaasIssuedLink) Expire(ctx context.Context) error {
	for _, link := range l.links {
		if err := l.client.DeletePaymentLink(ctx, link.id, link.reference); err != nil {
			return err
		}
	}
	return nil
}

// AsaasWebhook receives the payment events configured in the Asaas panel. The
// event payload only identifies the charge; its state and payment link are read
// back from the authenticated API, and the link identifies the invoice that
// issued it. Permanent mismatches are acknowledged with 200 because
// Asaas pauses the whole webhook queue after repeated non-2xx responses.
func AsaasWebhook(ctx iris.Context) {
	webhookToken := strings.TrimSpace(env.Backend["ASAAS_WEBHOOK_TOKEN"])
	if !asaasConfigured() || webhookToken == "" {
		writeJSONError(ctx, iris.StatusServiceUnavailable, "webhook do Asaas não configurado")
		return
	}
	providedToken := strings.TrimSpace(ctx.GetHeader("asaas-access-token"))
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(webhookToken)) != 1 {
		writeJSONError(ctx, iris.StatusUnauthorized, "token do webhook inválido")
		return
	}
	rawBody, err := io.ReadAll(http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, 1<<20))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "payload do webhook inválido")
		return
	}
	var event struct {
		ID      string `json:"id"`
		Event   string `json:"event"`
		Payment *struct {
			ID          string `json:"id"`
			PaymentLink string `json:"paymentLink"`
		} `json:"payment"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "evento do webhook inválido")
		return
	}
	eventName := strings.ToUpper(strings.TrimSpace(event.Event))
	if !strings.HasPrefix(eventName, "PAYMENT_") || event.Payment == nil {
		ctx.StatusCode(iris.StatusOK)
		return
	}
	providerPaymentID := strings.TrimSpace(event.Payment.ID)
	if providerPaymentID == "" {
		writeJSONError(ctx, iris.StatusBadRequest, "identificador da cobrança ausente")
		return
	}
	eventLinkID := strings.TrimSpace(event.Payment.PaymentLink)
	if eventLinkID == "" {
		// Charges created outside the invoice links are not managed here.
		ctx.StatusCode(iris.StatusOK)
		return
	}

	client := newAsaasClient(webhookProviderRequestTimeout)
	providerPayment, err := client.GetPayment(ctx.Request().Context(), providerPaymentID)
	if err != nil {
		log.Printf("Asaas webhook confirmation failed: payment=%s error=%v", providerPaymentID, err)
		writeJSONError(ctx, iris.StatusServiceUnavailable, "não foi possível confirmar a cobrança no Asaas; tente novamente")
		return
	}
	if providerPayment.PaymentLink != eventLinkID {
		acknowledgeIgnoredAsaasEvent(ctx, "payment link differs from event: payment=%s", providerPaymentID)
		return
	}
	mappedStatus, err := repo.MapAsaasPaymentStatus(providerPayment.Status)
	if err != nil {
		acknowledgeIgnoredAsaasEvent(ctx, "unknown status: payment=%s status=%s", providerPaymentID, providerPayment.Status)
		return
	}
	if providerPayment.Deleted || (mappedStatus != repo.PaymentStatusPaid && mappedStatus != repo.PaymentStatusRefunded) {
		// Pending, overdue and in-progress charges do not change the invoice:
		// its link can still be paid with a new PIX, boleto or card charge.
		ctx.StatusCode(iris.StatusOK)
		return
	}

	internalPayment, err := asaasGetPayment(providerPayment.PaymentLink)
	if errors.Is(err, sql.ErrNoRows) {
		acknowledgeIgnoredAsaasEvent(ctx, "no invoice issued this link: link=%s", providerPayment.PaymentLink)
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar a cobrança")
		return
	}
	internalPaymentID := internalPayment.Id
	if internalPayment.CheckoutKey == nil || strings.TrimSpace(*internalPayment.CheckoutKey) == "" {
		acknowledgeIgnoredAsaasEvent(ctx, "invoice without checkout key: payment=%d", internalPaymentID)
		return
	}
	amountCents, err := payments.AsaasAmountCents(providerPayment.Value)
	if err != nil || amountCents != internalPayment.AmountCents {
		acknowledgeIgnoredAsaasEvent(ctx, "amount does not match invoice: payment=%d charge=%s value=%s", internalPaymentID, providerPayment.ID, providerPayment.Value)
		return
	}

	eventID := strings.TrimSpace(event.ID)
	if eventID == "" {
		digest := sha256.Sum256(rawBody)
		eventID = "payload:" + hex.EncodeToString(digest[:])
	}
	// The transaction receipt is often empty (e.g. in the sandbox); the charge
	// invoice page then serves as the receipt, never the payment link.
	var receiptURL *string
	if value := strings.TrimSpace(providerPayment.TransactionReceiptURL); value != "" {
		receiptURL = &value
	} else if value := strings.TrimSpace(providerPayment.InvoiceURL); value != "" {
		receiptURL = &value
	}
	_, _, err = asaasApplyPaymentEvent(eventID, internalPaymentID, repo.PaymentProviderAsaas,
		providerPayment.ID, mappedStatus, nil, receiptURL)
	switch {
	case errors.Is(err, repo.ErrDiscardedPayment), errors.Is(err, repo.ErrManuallyConfirmedPayment),
		// A second charge through the same link after the invoice was settled
		// becomes company credit, and its refund reverses that credit.
		errors.Is(err, repo.ErrProviderPaymentMismatch):
		auditErr := asaasRecordSurplusEvent(internalPayment.CompanyId, internalPaymentID, repo.PaymentProviderAsaas,
			eventID, providerPayment.ID, mappedStatus)
		if errors.Is(auditErr, repo.ErrProviderPaymentMismatch) {
			acknowledgeIgnoredAsaasEvent(ctx, "charge is not surplus for invoice: payment=%d charge=%s", internalPaymentID, providerPayment.ID)
			return
		}
		if auditErr != nil {
			log.Printf("Asaas surplus payment audit failed: payment=%d: %v", internalPaymentID, auditErr)
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível registrar o pagamento excedente")
			return
		}
		log.Printf("Asaas surplus charge recorded: payment=%d charge=%s status=%s", internalPaymentID, providerPayment.ID, mappedStatus)
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível aplicar o pagamento")
		return
	}
	if mappedStatus == repo.PaymentStatusPaid {
		// Stops the links of the paid invoice from accepting another charge.
		links, listErr := asaasInvoiceLinks(internalPayment)
		if listErr != nil {
			log.Printf("Asaas paid invoice links lookup failed: payment=%d error=%v", internalPaymentID, listErr)
		}
		for _, link := range links {
			if deleteErr := client.DeletePaymentLink(ctx.Request().Context(), link.id, link.reference); deleteErr != nil {
				log.Printf("Asaas paid link deactivation failed: payment=%d link=%s error=%v", internalPaymentID, link.id, deleteErr)
			}
		}
	}
	ctx.StatusCode(iris.StatusOK)
}

func acknowledgeIgnoredAsaasEvent(ctx iris.Context, format string, args ...any) {
	log.Printf("ignored Asaas webhook: "+format, args...)
	ctx.StatusCode(iris.StatusOK)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
