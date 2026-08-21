package repo

import (
	"errors"
	"testing"
	"time"
)

func TestMapMercadoPagoPaymentStatus(t *testing.T) {
	tests := []struct {
		providerStatus string
		want           string
	}{
		{providerStatus: "approved", want: PaymentStatusPaid},
		{providerStatus: " PENDING ", want: PaymentStatusPending},
		{providerStatus: "in_process", want: PaymentStatusProcessing},
		{providerStatus: "rejected", want: PaymentStatusFailed},
		{providerStatus: "cancelled", want: PaymentStatusCanceled},
		{providerStatus: "refunded", want: PaymentStatusRefunded},
		{providerStatus: "charged_back", want: PaymentStatusRefunded},
	}

	for _, test := range tests {
		t.Run(test.providerStatus, func(t *testing.T) {
			got, err := MapMercadoPagoPaymentStatus(test.providerStatus)
			if err != nil {
				t.Fatalf("MapMercadoPagoPaymentStatus(%q) returned error: %v", test.providerStatus, err)
			}
			if got != test.want {
				t.Fatalf("MapMercadoPagoPaymentStatus(%q) = %q, want %q", test.providerStatus, got, test.want)
			}
		})
	}

	if _, err := MapMercadoPagoPaymentStatus("unknown"); !errors.Is(err, ErrUnknownMercadoPagoStatus) {
		t.Fatalf("unknown status error = %v, want %v", err, ErrUnknownMercadoPagoStatus)
	}
}

func TestMapAbacatePayPaymentStatus(t *testing.T) {
	tests := []struct {
		providerStatus string
		want           string
	}{
		{providerStatus: "PENDING", want: PaymentStatusPending},
		{providerStatus: " paid ", want: PaymentStatusPaid},
		{providerStatus: "EXPIRED", want: PaymentStatusCanceled},
		{providerStatus: "CANCELLED", want: PaymentStatusCanceled},
		{providerStatus: "REFUNDED", want: PaymentStatusRefunded},
	}

	for _, test := range tests {
		t.Run(test.providerStatus, func(t *testing.T) {
			got, err := MapAbacatePayPaymentStatus(test.providerStatus)
			if err != nil {
				t.Fatalf("MapAbacatePayPaymentStatus(%q) returned error: %v", test.providerStatus, err)
			}
			if got != test.want {
				t.Fatalf("MapAbacatePayPaymentStatus(%q) = %q, want %q", test.providerStatus, got, test.want)
			}
		})
	}

	if _, err := MapAbacatePayPaymentStatus("unknown"); !errors.Is(err, ErrUnknownAbacatePayStatus) {
		t.Fatalf("unknown status error = %v, want %v", err, ErrUnknownAbacatePayStatus)
	}
}

func TestAdvanceSubscriptionPeriodEnd(t *testing.T) {
	currentEnd := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	paidEarly := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if got, want := advanceSubscriptionPeriodEnd(currentEnd, paidEarly, 30), time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("early payment end = %v, want %v", got, want)
	}

	paidLate := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	if got, want := advanceSubscriptionPeriodEnd(currentEnd, paidLate, 30), time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("late payment end = %v, want %v", got, want)
	}
	if got, want := advanceSubscriptionPeriodEnd(currentEnd, paidEarly, 365), time.Date(2027, time.August, 31, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("annual payment end = %v, want %v", got, want)
	}
}

func TestBillingCycleTerms(t *testing.T) {
	plan := Plan{MonthlyPriceCents: 4900, AnnualPriceCents: 49900}
	price, days, err := BillingCycleTerms(plan, BillingCycleMonthly)
	if err != nil || price != 4900 || days != 30 {
		t.Fatalf("monthly terms = price %d days %d error %v", price, days, err)
	}
	price, days, err = BillingCycleTerms(plan, BillingCycleAnnual)
	if err != nil || price != 49900 || days != 365 {
		t.Fatalf("annual terms = price %d days %d error %v", price, days, err)
	}
	if _, _, err := BillingCycleTerms(plan, "weekly"); !errors.Is(err, ErrInvalidBillingCycle) {
		t.Fatalf("invalid cycle error = %v", err)
	}
}

func TestRecomputePeriodEndAfterRefund(t *testing.T) {
	baseEnd := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	firstLatePayment := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	secondLatePayment := time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC)

	withBoth := recomputePeriodEnd(baseEnd, []time.Time{firstLatePayment, secondLatePayment}, 30)
	if want := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC); !withBoth.Equal(want) {
		t.Fatalf("stacked period end = %v, want %v", withBoth, want)
	}
	afterFirstRefund := recomputePeriodEnd(baseEnd, []time.Time{secondLatePayment}, 30)
	if want := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC); !afterFirstRefund.Equal(want) {
		t.Fatalf("period end after first refund = %v, want %v", afterFirstRefund, want)
	}
	afterSecondRefund := recomputePeriodEnd(baseEnd, []time.Time{firstLatePayment}, 30)
	if want := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC); !afterSecondRefund.Equal(want) {
		t.Fatalf("period end after second refund = %v, want %v", afterSecondRefund, want)
	}
}

func TestPaymentDueDate(t *testing.T) {
	now := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	futureEnd := now.Add(48 * time.Hour)
	if got := paymentDueDate(futureEnd, now); !got.Equal(futureEnd) {
		t.Fatalf("future due date = %v, want %v", got, futureEnd)
	}
	if got, want := paymentDueDate(now.Add(-time.Hour), now), now.AddDate(0, 0, 1); !got.Equal(want) {
		t.Fatalf("expired due date = %v, want %v", got, want)
	}
}

func TestNextPaymentStatusPreservesTerminalTransitions(t *testing.T) {
	if got := nextPaymentStatus(PaymentStatusPaid, PaymentStatusProcessing); got != PaymentStatusPaid {
		t.Fatalf("paid payment regressed to %q", got)
	}
	if got := nextPaymentStatus(PaymentStatusPaid, PaymentStatusRefunded); got != PaymentStatusRefunded {
		t.Fatalf("paid payment refund mapped to %q", got)
	}
	if got := nextPaymentStatus(PaymentStatusRefunded, PaymentStatusPaid); got != PaymentStatusRefunded {
		t.Fatalf("refunded payment changed to %q", got)
	}
}
