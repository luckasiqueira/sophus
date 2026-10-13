package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"sophus/internal/payments"
	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func TestDecimalNumberToCents(t *testing.T) {
	tests := []struct {
		name    string
		value   json.Number
		want    int64
		wantErr bool
	}{
		{name: "integer", value: json.Number("42"), want: 4200},
		{name: "one decimal place", value: json.Number("42.5"), want: 4250},
		{name: "two decimal places", value: json.Number("42.05"), want: 4205},
		{name: "invalid", value: json.Number("not-a-number"), wantErr: true},
		{name: "more than two decimal places", value: json.Number("42.005"), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decimalNumberToCents(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("decimalNumberToCents(%q) error = nil, want error", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("decimalNumberToCents(%q) error = %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("decimalNumberToCents(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestAbacatePayWebhookCheckoutID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "billing payload", raw: `{"billing":{"id":"bill_123"}}`, want: "bill_123"},
		{name: "checkout payload", raw: `{"checkout":{"id":"bill_456"}}`, want: "bill_456"},
		{name: "direct checkout payload", raw: `{"id":"bill_789"}`, want: "bill_789"},
		{name: "billing takes precedence", raw: `{"id":"bill_direct","billing":{"id":"bill_nested"}}`, want: "bill_nested"},
		{name: "malformed", raw: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := abacatePayWebhookCheckoutID(json.RawMessage(test.raw)); got != test.want {
				t.Fatalf("abacatePayWebhookCheckoutID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAbacatePayCheckoutIdentityComplete(t *testing.T) {
	key, id, blank := "secret", "bill_123", "  "
	for _, test := range []struct {
		name    string
		payment repo.Payment
		want    bool
	}{
		{"missing key", repo.Payment{ProviderPreferenceId: &id}, false},
		{"missing ID", repo.Payment{CheckoutKey: &key}, false},
		{"blank ID", repo.Payment{CheckoutKey: &key, ProviderPreferenceId: &blank}, false},
		{"blank key", repo.Payment{CheckoutKey: &blank, ProviderPreferenceId: &id}, false},
		{"preference ID", repo.Payment{CheckoutKey: &key, ProviderPreferenceId: &id}, true},
		{"payment ID", repo.Payment{CheckoutKey: &key, ProviderPaymentId: &id}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := abacatePayCheckoutIdentityComplete(test.payment); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestPendingCheckoutIsNotMistakenForPaidInvoice(t *testing.T) {
	id, other := "bill_123", "bill_other"
	for _, test := range []struct {
		name    string
		payment repo.Payment
		want    bool
	}{
		{"pix checkout ID", repo.Payment{Status: repo.PaymentStatusPending, Provider: repo.PaymentProviderAbacatePay, ProviderPaymentId: &id, ProviderPreferenceId: &id}, true},
		{"pix payment ID different", repo.Payment{Status: repo.PaymentStatusPending, Provider: repo.PaymentProviderAbacatePay, ProviderPaymentId: &id, ProviderPreferenceId: &other}, false},
		{"mercado pago charge", repo.Payment{Status: repo.PaymentStatusPending, Provider: repo.PaymentProviderMercadoPago, ProviderPaymentId: &id, ProviderPreferenceId: &id}, false},
		{"settled pix invoice", repo.Payment{Status: repo.PaymentStatusPaid, Provider: repo.PaymentProviderAbacatePay, ProviderPaymentId: &id, ProviderPreferenceId: &id}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := canManagePendingCompanyPayment(test.payment); got != test.want {
				t.Fatalf("canManagePendingCompanyPayment() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAbacatePayWebhookCheckoutPayload(t *testing.T) {
	raw := json.RawMessage(`{"checkout":{"id":"bill_123","externalId":"4","url":"https://app.abacatepay.com/pay/bill_123","amount":100,"paidAmount":100,"status":"PAID","receiptUrl":"https://app.abacatepay.com/receipt/transaction","metadata":{"checkout_key":"secret"},"updatedAt":"2026-09-23T11:40:55.713Z"}}`)
	checkout, complete := abacatePayWebhookCheckout(raw)
	if !complete {
		t.Fatal("checkout payload was not recognized as complete")
	}
	if checkout.ID != "bill_123" || checkout.ExternalID != "4" || checkout.Status != "PAID" || checkout.PaidAmount != 100 || checkout.Metadata["checkout_key"] != "secret" {
		t.Fatalf("unexpected checkout: %#v", checkout)
	}
}

func TestAbacatePayCheckoutsMatch(t *testing.T) {
	eventCheckout := payments.AbacateCheckout{ID: "bill_123", ExternalID: "4", Amount: 100, Metadata: map[string]string{"checkout_key": "secret"}}
	providerCheckout := eventCheckout
	providerCheckout.Status = "PAID"
	providerCheckout.PaidAmount = 100
	if !abacatePayCheckoutsMatch(eventCheckout, providerCheckout) {
		t.Fatal("matching checkouts were rejected")
	}
	providerCheckout.Amount = 200
	if abacatePayCheckoutsMatch(eventCheckout, providerCheckout) {
		t.Fatal("different amounts were accepted")
	}
	providerCheckout = eventCheckout
	providerCheckout.Metadata = map[string]string{"checkout_key": "other"}
	if abacatePayCheckoutsMatch(eventCheckout, providerCheckout) {
		t.Fatal("different checkout keys were accepted")
	}
}

func TestValidateAbacatePayCheckout(t *testing.T) {
	payment := repo.Payment{Id: 42, AmountCents: 8900}
	checkout := payments.AbacateCheckout{
		ID:         "bill_123",
		ExternalID: "42",
		URL:        "https://pay.example/bill_123",
		Amount:     8900,
		Metadata:   map[string]string{"checkout_key": "secret"},
	}
	if err := validateAbacatePayCheckout(payment, checkout, "secret"); err != nil {
		t.Fatalf("valid checkout returned error: %v", err)
	}
	checkout.Amount++
	if err := validateAbacatePayCheckout(payment, checkout, "secret"); err == nil {
		t.Fatal("checkout with wrong amount returned nil error")
	}
	checkout.Amount = payment.AmountCents
	if err := validateAbacatePayCheckout(payment, checkout, "other-secret"); err == nil {
		t.Fatal("checkout with wrong key returned nil error")
	}
}

func TestRandomCheckoutKey(t *testing.T) {
	first, err := randomCheckoutKey()
	if err != nil {
		t.Fatalf("randomCheckoutKey: %v", err)
	}
	second, err := randomCheckoutKey()
	if err != nil {
		t.Fatalf("randomCheckoutKey: %v", err)
	}
	if len(first) != 64 || len(second) != 64 || first == second {
		t.Fatalf("unexpected checkout keys: %q %q", first, second)
	}
}

func TestBillingSettingsURLPreservesReturnParameters(t *testing.T) {
	got := billingSettingsURL("ignored=value&reason=storage_quota&payment=pending")
	want := "/settings?payment=pending&reason=storage_quota#settings-billing"
	if got != want {
		t.Fatalf("billingSettingsURL() = %q, want %q", got, want)
	}
}

func TestPaymentReturnURLPlacesQueryBeforeFragment(t *testing.T) {
	got := paymentReturnURL("https://app.example", "/settings#settings-billing", "success")
	want := "https://app.example/settings?payment=success#settings-billing"
	if got != want {
		t.Fatalf("paymentReturnURL() = %q, want %q", got, want)
	}
}

func TestPaymentCheckoutExpiration(t *testing.T) {
	now := time.Date(2026, time.September, 23, 12, 0, 0, 0, time.UTC)
	future := now.AddDate(0, 0, 10)
	if got := paymentCheckoutExpiration(future, now); !got.Equal(future) {
		t.Fatalf("future expiration = %v, want %v", got, future)
	}
	want := now.AddDate(0, 0, 1)
	if got := paymentCheckoutExpiration(now.Add(-time.Minute), now); !got.Equal(want) {
		t.Fatalf("overdue expiration = %v, want %v", got, want)
	}
}

func TestBillingPageRedirectsAuthenticatedCompanyAgent(t *testing.T) {
	app := iris.New()
	app.Get("/billing", func(ctx iris.Context) {
		ctx.Values().Set("authenticatedAgent", repo.Agent{Id: 42, CompanyId: 7})
		BillingPage(ctx)
	})
	if err := app.Build(); err != nil {
		t.Fatalf("build routes: %v", err)
	}
	request := httptest.NewRequest("GET", "/billing?ignored=value&reason=storage_quota&payment=pending", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != iris.StatusFound {
		t.Fatalf("GET /billing status = %d, want %d", response.Code, iris.StatusFound)
	}
	want := "/settings?payment=pending&reason=storage_quota#settings-billing"
	if got := response.Header().Get("Location"); got != want {
		t.Fatalf("GET /billing Location = %q, want %q", got, want)
	}
}
