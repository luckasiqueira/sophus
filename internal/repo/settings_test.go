package repo

import "testing"

func TestIsValidPaymentProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{provider: PaymentProviderMercadoPago, want: true},
		{provider: PaymentProviderAbacatePay, want: true},
		{provider: PaymentProviderAsaas, want: true},
		{provider: ""},
		{provider: "stripe"},
		{provider: "MERCADO_PAGO"},
		{provider: "ASAAS"},
	}

	for _, test := range tests {
		if got := IsValidPaymentProvider(test.provider); got != test.want {
			t.Fatalf("IsValidPaymentProvider(%q) = %t, want %t", test.provider, got, test.want)
		}
	}
}

func TestEffectivePaymentProvider(t *testing.T) {
	abacatePay := PaymentProviderAbacatePay
	invalid := "stripe"
	tests := []struct {
		name          string
		allowOverride bool
		override      *string
		want          string
	}{
		{name: "no override", allowOverride: true, want: PaymentProviderMercadoPago},
		{name: "override disabled", override: &abacatePay, want: PaymentProviderMercadoPago},
		{name: "valid override", allowOverride: true, override: &abacatePay, want: PaymentProviderAbacatePay},
		{name: "invalid override", allowOverride: true, override: &invalid, want: PaymentProviderMercadoPago},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := effectivePaymentProvider(PaymentProviderMercadoPago, test.allowOverride, test.override)
			if got != test.want {
				t.Fatalf("effectivePaymentProvider() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidMediaRetention(t *testing.T) {
	if !IsValidMediaRetention(nil) {
		t.Fatal("disabled retention must be valid")
	}
	for _, days := range []int{30, 90, 120} {
		value := days
		if !IsValidMediaRetention(&value) {
			t.Fatalf("retention %d rejected", days)
		}
	}
	for _, days := range []int{0, 31, 365} {
		value := days
		if IsValidMediaRetention(&value) {
			t.Fatalf("retention %d accepted", days)
		}
	}
}
