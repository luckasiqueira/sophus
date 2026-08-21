package controllers

import (
	"encoding/json"
	"testing"

	"sophus/internal/payments"
	"sophus/internal/repo"
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
