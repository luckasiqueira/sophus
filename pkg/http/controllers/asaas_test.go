package controllers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
)

type fakeAsaasLink struct {
	reference   string
	billingType string
	deleted     bool
}

type fakeAsaasProvider struct {
	mu            sync.Mutex
	paymentStatus string
	paymentValue  string
	paymentLink   string
	receiptURL    string
	links         map[string]*fakeAsaasLink
	created       []string
	requests      []string
}

func (f *fakeAsaasProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("access_token") != "asaas-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	linkID := strings.TrimPrefix(r.URL.Path, "/paymentLinks/")
	link := f.links[linkID]
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/payments/pay_"):
		chargeID := strings.TrimPrefix(r.URL.Path, "/payments/")
		fmt.Fprintf(w, `{"id":%q,"status":%q,"value":%s,"billingType":"PIX","paymentLink":%q,"invoiceUrl":"https://asaas.test/i/%s","transactionReceiptUrl":%q}`,
			chargeID, f.paymentStatus, f.paymentValue, f.paymentLink, chargeID, f.receiptURL)
	case r.Method == http.MethodPost && r.URL.Path == "/paymentLinks":
		var payload struct {
			BillingType       string      `json:"billingType"`
			Value             json.Number `json:"value"`
			ExternalReference string      `json:"externalReference"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		id := fmt.Sprintf("lnk_%d", len(f.links)+1)
		f.links[id] = &fakeAsaasLink{reference: payload.ExternalReference, billingType: payload.BillingType}
		f.created = append(f.created, payload.BillingType)
		fmt.Fprintf(w, `{"id":%q,"url":"https://asaas.test/c/%s","value":%s,"billingType":%q,"externalReference":%q}`,
			id, id, payload.Value, payload.BillingType, payload.ExternalReference)
	case r.Method == http.MethodGet && link != nil:
		fmt.Fprintf(w, `{"id":%q,"url":"https://asaas.test/c/%s","billingType":%q,"externalReference":%q,"deleted":%t}`,
			linkID, linkID, link.billingType, link.reference, link.deleted)
	case r.Method == http.MethodDelete && link != nil:
		link.deleted = true
		fmt.Fprintf(w, `{"id":%q,"deleted":true}`, linkID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAsaasProvider) isDeleted(linkID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[linkID] != nil && f.links[linkID].deleted
}

func (f *fakeAsaasProvider) setDeleted(linkID string, deleted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[linkID].deleted = deleted
}

func (f *fakeAsaasProvider) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// fakeAsaasInvoices mirrors the idempotency of provider_webhook_events.
type fakeAsaasInvoices struct {
	payment repo.Payment
	links   []repo.AsaasPaymentLink
	events  map[string]bool
	applied int
	credits map[string]int64
	balance int64
}

func (f *fakeAsaasInvoices) get(linkID string) (repo.Payment, error) {
	if f.payment.ProviderPreferenceId != nil && *f.payment.ProviderPreferenceId == linkID {
		return f.payment, nil
	}
	for _, link := range f.links {
		if link.LinkID == linkID && link.PaymentID == f.payment.Id {
			return f.payment, nil
		}
	}
	return repo.Payment{}, sql.ErrNoRows
}

func (f *fakeAsaasInvoices) list(paymentID int) ([]repo.AsaasPaymentLink, error) {
	var links []repo.AsaasPaymentLink
	for index := len(f.links) - 1; index >= 0; index-- {
		if f.links[index].PaymentID == paymentID {
			links = append(links, f.links[index])
		}
	}
	return links, nil
}

func (f *fakeAsaasInvoices) record(link repo.AsaasPaymentLink) error {
	for _, existing := range f.links {
		if existing.LinkID == link.LinkID {
			return nil
		}
	}
	f.links = append(f.links, link)
	return nil
}

// switchCheckout mirrors the guards of repo.SwitchAsaasCheckout.
func (f *fakeAsaasInvoices) switchCheckout(paymentID, companyID int, checkoutKey, linkID, checkoutURL string) (repo.Payment, error) {
	mapped := false
	for _, link := range f.links {
		mapped = mapped || (link.LinkID == linkID && link.PaymentID == paymentID)
	}
	if !mapped || paymentID != f.payment.Id || companyID != f.payment.CompanyId || f.payment.Status != repo.PaymentStatusPending ||
		f.payment.ProviderPaymentId != nil || f.payment.CheckoutKey == nil || *f.payment.CheckoutKey != checkoutKey {
		return repo.Payment{}, sql.ErrNoRows
	}
	f.payment.ProviderPreferenceId, f.payment.CheckoutURL = &linkID, &checkoutURL
	return f.payment, nil
}

func (f *fakeAsaasInvoices) apply(eventID string, paymentID int, provider, providerPaymentID, status string, _ *time.Time, receiptURL *string) (repo.Payment, bool, error) {
	if paymentID != f.payment.Id || provider != repo.PaymentProviderAsaas {
		return repo.Payment{}, false, sql.ErrNoRows
	}
	if f.events[eventID] {
		return f.payment, false, nil
	}
	if f.payment.ProviderPaymentId != nil && *f.payment.ProviderPaymentId != providerPaymentID {
		// The repository rolls back, so the event is not marked as processed.
		return repo.Payment{}, false, repo.ErrProviderPaymentMismatch
	}
	f.events[eventID] = true
	f.applied++
	firstPaid := status == repo.PaymentStatusPaid && f.payment.PaidAt == nil
	f.payment.Status = status
	f.payment.ProviderPaymentId = &providerPaymentID
	f.payment.ReceiptURL = receiptURL
	if firstPaid {
		now := time.Now()
		f.payment.PaidAt = &now
	}
	return f.payment, firstPaid, nil
}

// recordSurplus mirrors RecordSurplusCheckoutEvent for a charge that differs
// from the one that settled the invoice: one credit per charge, reversed on refund.
func (f *fakeAsaasInvoices) recordSurplus(companyID, paymentID int, provider, eventID, transactionID, status string) error {
	if f.events[eventID] {
		return nil
	}
	if companyID != f.payment.CompanyId || paymentID != f.payment.Id || provider != repo.PaymentProviderAsaas ||
		f.payment.ProviderPaymentId == nil || *f.payment.ProviderPaymentId == transactionID {
		return repo.ErrProviderPaymentMismatch
	}
	f.events[eventID] = true
	switch status {
	case repo.PaymentStatusPaid:
		if _, reversed := f.credits[transactionID+":reversal"]; !reversed {
			if _, credited := f.credits[transactionID+":credit"]; !credited {
				f.credits[transactionID+":credit"] = f.payment.AmountCents
				f.balance += f.payment.AmountCents
			}
		}
	case repo.PaymentStatusRefunded:
		if _, reversed := f.credits[transactionID+":reversal"]; !reversed {
			reversal := int64(0)
			if _, credited := f.credits[transactionID+":credit"]; credited {
				reversal = -f.payment.AmountCents
			}
			f.credits[transactionID+":reversal"] = reversal
			f.balance += reversal
		}
	}
	return nil
}

func setupAsaasWebhookTest(t *testing.T) (*fakeAsaasProvider, *fakeAsaasInvoices, *iris.Application) {
	t.Helper()
	provider := &fakeAsaasProvider{paymentStatus: "RECEIVED", paymentValue: "49.9", paymentLink: "lnk_1", receiptURL: "https://asaas.test/receipt",
		links: map[string]*fakeAsaasLink{"lnk_1": {reference: "42:checkout-key", billingType: "UNDEFINED"}}}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)

	previousEnv := map[string]string{}
	for key, value := range map[string]string{
		"ASAAS_API_KEY": "asaas-key", "ASAAS_WEBHOOK_TOKEN": "webhook-token", "ASAAS_BASE_URL": server.URL,
	} {
		previousEnv[key] = env.Backend[key]
		env.Backend[key] = value
	}
	t.Cleanup(func() {
		for key, value := range previousEnv {
			env.Backend[key] = value
		}
	})

	linkID, checkoutKey, checkoutURL := "lnk_1", "checkout-key", "https://asaas.test/c/lnk_1"
	invoices := &fakeAsaasInvoices{
		payment: repo.Payment{Id: 42, CompanyId: 7, Status: repo.PaymentStatusPending, AmountCents: 4990,
			Provider: repo.PaymentProviderAsaas, ProviderPreferenceId: &linkID, CheckoutKey: &checkoutKey, CheckoutURL: &checkoutURL},
		events:  map[string]bool{},
		credits: map[string]int64{},
	}
	originalGet, originalList, originalRecord, originalSwitch := asaasGetPayment, asaasListPaymentLinks, asaasRecordPaymentLink, asaasSwitchCheckout
	originalApply, originalSurplus := asaasApplyPaymentEvent, asaasRecordSurplusEvent
	asaasGetPayment = invoices.get
	asaasListPaymentLinks = invoices.list
	asaasRecordPaymentLink = invoices.record
	asaasSwitchCheckout = invoices.switchCheckout
	asaasApplyPaymentEvent = invoices.apply
	asaasRecordSurplusEvent = invoices.recordSurplus
	t.Cleanup(func() {
		asaasGetPayment, asaasListPaymentLinks, asaasRecordPaymentLink, asaasSwitchCheckout = originalGet, originalList, originalRecord, originalSwitch
		asaasApplyPaymentEvent, asaasRecordSurplusEvent = originalApply, originalSurplus
	})

	app := iris.New()
	app.Post("/payments/asaas/webhook", AsaasWebhook)
	app.Post("/test/switch/{method}", func(ctx iris.Context) {
		company, plan := repo.Company{Id: 7, Name: "Acme"}, repo.Plan{Id: 3, Name: "Pro"}
		if _, err := switchAsaasCheckoutMethod(ctx, company, plan, invoices.payment, ctx.Params().Get("method")); err != nil {
			writeCheckoutError(ctx, err)
		}
	})
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	return provider, invoices, app
}

func postAsaasWebhook(app *iris.Application, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/payments/asaas/webhook", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("asaas-access-token", token)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

const asaasReceivedEvent = `{"id":"evt_1","event":"PAYMENT_RECEIVED","payment":{"id":"pay_1","paymentLink":"lnk_1","status":"RECEIVED"}}`

func TestAsaasBaseURLFollowsSandboxFlag(t *testing.T) {
	previousBaseURL, previousSandbox := env.Backend["ASAAS_BASE_URL"], env.Backend["ASAAS_SANDBOX"]
	t.Cleanup(func() {
		env.Backend["ASAAS_BASE_URL"], env.Backend["ASAAS_SANDBOX"] = previousBaseURL, previousSandbox
	})
	for _, test := range []struct {
		baseURL, sandbox, want string
	}{
		{sandbox: "", want: ""},
		{sandbox: "false", want: ""},
		{sandbox: "true", want: "https://api-sandbox.asaas.com/v3"},
		{sandbox: " TRUE ", want: "https://api-sandbox.asaas.com/v3"},
		{sandbox: "1", want: "https://api-sandbox.asaas.com/v3"},
		{sandbox: "sim", want: ""},
		{baseURL: "http://proxy.test/v3", sandbox: "true", want: "http://proxy.test/v3"},
	} {
		env.Backend["ASAAS_BASE_URL"], env.Backend["ASAAS_SANDBOX"] = test.baseURL, test.sandbox
		if got := asaasBaseURL(); got != test.want {
			t.Errorf("ASAAS_BASE_URL=%q ASAAS_SANDBOX=%q: base URL = %q, want %q", test.baseURL, test.sandbox, got, test.want)
		}
	}
}

func TestAsaasWebhookRejectsInvalidToken(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	for _, token := range []string{"", "wrong-token", "webhook-token-extra"} {
		response := postAsaasWebhook(app, token, asaasReceivedEvent)
		if response.Code != iris.StatusUnauthorized {
			t.Fatalf("token %q status = %d, want %d", token, response.Code, iris.StatusUnauthorized)
		}
	}
	if calls := provider.calls(); len(calls) != 0 || invoices.applied != 0 {
		t.Fatalf("unauthenticated webhook reached provider %v or applied %d events", calls, invoices.applied)
	}
}

func TestAsaasWebhookRequiresConfiguration(t *testing.T) {
	_, _, app := setupAsaasWebhookTest(t)
	env.Backend["ASAAS_WEBHOOK_TOKEN"] = ""
	if response := postAsaasWebhook(app, "", asaasReceivedEvent); response.Code != iris.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, iris.StatusServiceUnavailable)
	}
}

func TestAsaasWebhookConfirmedPaymentMarksInvoicePaid(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent)
	if response.Code != iris.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	payment := invoices.payment
	if payment.Status != repo.PaymentStatusPaid || payment.PaidAt == nil || payment.ProviderPaymentId == nil || *payment.ProviderPaymentId != "pay_1" {
		t.Fatalf("invoice was not marked paid: %#v", payment)
	}
	if payment.ReceiptURL == nil || *payment.ReceiptURL != "https://asaas.test/receipt" {
		t.Fatalf("receipt URL = %v", payment.ReceiptURL)
	}
	if !provider.isDeleted("lnk_1") {
		t.Fatalf("paid link was not deactivated; calls = %v", provider.calls())
	}
}

func TestAsaasWebhookIgnoresDuplicateEvent(t *testing.T) {
	_, invoices, app := setupAsaasWebhookTest(t)
	for attempt := 0; attempt < 2; attempt++ {
		if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, response.Code, response.Body.String())
		}
	}
	if invoices.applied != 1 {
		t.Fatalf("applied = %d, want the duplicate event to be ignored", invoices.applied)
	}
	if invoices.payment.Status != repo.PaymentStatusPaid {
		t.Fatalf("status = %q", invoices.payment.Status)
	}
}

func TestAsaasWebhookUsesProviderStateInsteadOfEventPayload(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	provider.paymentStatus = "PENDING"
	if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.applied != 0 || invoices.payment.Status != repo.PaymentStatusPending {
		t.Fatalf("pending charge changed the invoice: %#v", invoices.payment)
	}
}

func TestAsaasWebhookKeepsInvoicePendingWhenChargeIsOverdue(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	provider.paymentStatus = "OVERDUE"
	body := `{"id":"evt_2","event":"PAYMENT_OVERDUE","payment":{"id":"pay_1","paymentLink":"lnk_1"}}`
	if response := postAsaasWebhook(app, "webhook-token", body); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.applied != 0 || invoices.payment.Status != repo.PaymentStatusPending || provider.isDeleted("lnk_1") {
		t.Fatalf("overdue charge changed the invoice: %#v", invoices.payment)
	}
}

func TestAsaasWebhookMarksRefundedPayment(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	postAsaasWebhook(app, "webhook-token", asaasReceivedEvent)
	provider.paymentStatus = "REFUNDED"
	body := `{"id":"evt_3","event":"PAYMENT_REFUNDED","payment":{"id":"pay_1","paymentLink":"lnk_1"}}`
	if response := postAsaasWebhook(app, "webhook-token", body); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.payment.Status != repo.PaymentStatusRefunded || invoices.applied != 2 {
		t.Fatalf("refund was not applied: %#v applied=%d", invoices.payment, invoices.applied)
	}
}

func TestAsaasWebhookIgnoresAmountMismatch(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	provider.paymentValue = "1.00"
	if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.applied != 0 || invoices.payment.Status != repo.PaymentStatusPending {
		t.Fatalf("mismatched amount changed the invoice: %#v", invoices.payment)
	}
}

func TestAsaasWebhookIgnoresChargesOutsideInvoiceLinks(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	body := `{"id":"evt_4","event":"PAYMENT_RECEIVED","payment":{"id":"pay_1"}}`
	if response := postAsaasWebhook(app, "webhook-token", body); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if calls := provider.calls(); len(calls) != 0 || invoices.applied != 0 {
		t.Fatalf("unrelated charge reached provider %v or applied %d", calls, invoices.applied)
	}
}

func TestAsaasWebhookRejectsEventWhoseLinkDiffersFromProvider(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	provider.paymentLink = "lnk_other"
	if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.applied != 0 {
		t.Fatalf("applied = %d, want 0", invoices.applied)
	}
}

func TestAsaasWebhookSecondChargeOnPaidInvoiceBecomesCredit(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	postAsaasWebhook(app, "webhook-token", asaasReceivedEvent)
	provider.setDeleted("lnk_1", false)

	second := `{"id":"evt_10","event":"PAYMENT_RECEIVED","payment":{"id":"pay_2","paymentLink":"lnk_1"}}`
	for attempt := 0; attempt < 2; attempt++ {
		if response := postAsaasWebhook(app, "webhook-token", second); response.Code != iris.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, response.Code, response.Body.String())
		}
	}
	// Card charges are reported as CONFIRMED and later RECEIVED: still one credit.
	confirmed := `{"id":"evt_11","event":"PAYMENT_CONFIRMED","payment":{"id":"pay_2","paymentLink":"lnk_1"}}`
	if response := postAsaasWebhook(app, "webhook-token", confirmed); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.balance != 4990 || invoices.credits["pay_2:credit"] != 4990 {
		t.Fatalf("balance = %d credits = %v, want one credit of 4990", invoices.balance, invoices.credits)
	}
	if invoices.payment.Status != repo.PaymentStatusPaid || *invoices.payment.ProviderPaymentId != "pay_1" || invoices.applied != 1 {
		t.Fatalf("surplus charge changed the settled invoice: %#v applied=%d", invoices.payment, invoices.applied)
	}
	if !provider.isDeleted("lnk_1") {
		t.Fatal("link that accepted a surplus charge was not deactivated")
	}

	provider.paymentStatus = "REFUNDED"
	refund := `{"id":"evt_12","event":"PAYMENT_REFUNDED","payment":{"id":"pay_2","paymentLink":"lnk_1"}}`
	if response := postAsaasWebhook(app, "webhook-token", refund); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.balance != 0 || invoices.payment.Status != repo.PaymentStatusPaid {
		t.Fatalf("refunded surplus balance = %d, invoice status = %q", invoices.balance, invoices.payment.Status)
	}
}

func TestAsaasWebhookManuallyConfirmedInvoiceCreditsCharge(t *testing.T) {
	_, invoices, app := setupAsaasWebhookTest(t)
	asaasApplyPaymentEvent = func(string, int, string, string, string, *time.Time, *string) (repo.Payment, bool, error) {
		return repo.Payment{}, false, repo.ErrManuallyConfirmedPayment
	}
	var recorded []string
	asaasRecordSurplusEvent = func(companyID, paymentID int, provider, eventID, transactionID, status string) error {
		recorded = append(recorded, fmt.Sprintf("%d/%d/%s/%s/%s/%s", companyID, paymentID, provider, eventID, transactionID, status))
		return nil
	}
	if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if len(recorded) != 1 || recorded[0] != "7/42/asaas/evt_1/pay_1/paid" || invoices.applied != 0 {
		t.Fatalf("recorded = %v", recorded)
	}
}

func switchAsaasMethod(app *iris.Application, method string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/test/switch/"+method, nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func TestAsaasSwitchCheckoutMethodCreatesAndReusesLinks(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	if response := switchAsaasMethod(app, "PIX"); response.Code != iris.StatusOK {
		t.Fatalf("switch to PIX status = %d, body = %s", response.Code, response.Body.String())
	}
	pixLink := *invoices.payment.ProviderPreferenceId
	if pixLink == "lnk_1" || *invoices.payment.CheckoutURL != "https://asaas.test/c/"+pixLink {
		t.Fatalf("invoice still points to %q", pixLink)
	}
	// The link issued before methods existed stays mapped so it can still settle the invoice.
	if len(invoices.links) != 2 || invoices.links[0].LinkID != "lnk_1" || invoices.links[0].BillingType != "UNDEFINED" ||
		invoices.links[1].LinkID != pixLink || invoices.links[1].BillingType != "PIX" || invoices.links[1].CheckoutKey != "checkout-key" {
		t.Fatalf("links = %#v", invoices.links)
	}

	if response := switchAsaasMethod(app, "BOLETO"); response.Code != iris.StatusOK {
		t.Fatalf("switch to boleto status = %d", response.Code)
	}
	boletoLink := *invoices.payment.ProviderPreferenceId
	if response := switchAsaasMethod(app, "PIX"); response.Code != iris.StatusOK {
		t.Fatalf("switch back to PIX status = %d", response.Code)
	}
	if *invoices.payment.ProviderPreferenceId != pixLink {
		t.Fatalf("PIX link was not reused: %q", *invoices.payment.ProviderPreferenceId)
	}
	switchAsaasMethod(app, "PIX")
	if got := strings.Join(provider.created, ","); got != "PIX,BOLETO" {
		t.Fatalf("created links = %s, want one per method", got)
	}
	if provider.isDeleted("lnk_1") || provider.isDeleted(pixLink) || provider.isDeleted(boletoLink) {
		t.Fatal("switching methods must keep earlier links payable")
	}
}

func TestAsaasWebhookSettlesInvoiceThroughEarlierMethodLink(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	switchAsaasMethod(app, "BOLETO")
	switchAsaasMethod(app, "PIX")
	// The boleto generated first is paid after the payer switched to PIX.
	provider.paymentLink = invoices.links[1].LinkID
	if response := postAsaasWebhook(app, "webhook-token", `{"id":"evt_20","event":"PAYMENT_RECEIVED","payment":{"id":"pay_9","paymentLink":"`+provider.paymentLink+`"}}`); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.payment.Status != repo.PaymentStatusPaid || *invoices.payment.ProviderPaymentId != "pay_9" {
		t.Fatalf("invoice was not settled by the earlier link: %#v", invoices.payment)
	}
	for _, link := range []string{"lnk_1", "lnk_2", "lnk_3"} {
		if !provider.isDeleted(link) {
			t.Fatalf("link %s of the paid invoice is still active", link)
		}
	}
}

func TestAsaasSwitchCheckoutMethodKeepsPaidInvoice(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	paid := "pay_1"
	invoices.payment.Status, invoices.payment.ProviderPaymentId = repo.PaymentStatusPaid, &paid
	if response := switchAsaasMethod(app, "PIX"); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if len(provider.created) != 0 || *invoices.payment.ProviderPreferenceId != "lnk_1" {
		t.Fatalf("paid invoice got a new link: created=%v", provider.created)
	}
}

func TestAsaasWebhookUsesInvoicePageWhenReceiptIsMissing(t *testing.T) {
	provider, invoices, app := setupAsaasWebhookTest(t)
	provider.receiptURL = ""
	if response := postAsaasWebhook(app, "webhook-token", asaasReceivedEvent); response.Code != iris.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if invoices.payment.ReceiptURL == nil || *invoices.payment.ReceiptURL != "https://asaas.test/i/pay_1" {
		t.Fatalf("receipt URL = %v, want the charge invoice page", invoices.payment.ReceiptURL)
	}
}
