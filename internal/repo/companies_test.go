package repo

import (
	"testing"
	"time"
)

func TestInitialSubscriptionTerms(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		priceCents   int64
		durationDays int
		trialDays    int
		wantStatus   string
		wantEnd      time.Time
	}{
		{name: "paid trial", priceCents: 4900, durationDays: 30, trialDays: 10, wantStatus: SubscriptionStatusTrialing, wantEnd: now.AddDate(0, 0, 10)},
		{name: "paid without trial", priceCents: 4900, durationDays: 30, wantStatus: SubscriptionStatusPastDue, wantEnd: now.Add(time.Microsecond)},
		{name: "free plan", durationDays: 30, trialDays: 10, wantStatus: SubscriptionStatusActive, wantEnd: now.AddDate(0, 0, 30)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, periodEnd := initialSubscriptionTerms(test.priceCents, test.durationDays, test.trialDays, now)
			if status != test.wantStatus || !periodEnd.Equal(test.wantEnd) {
				t.Fatalf("initial terms = (%q, %v), want (%q, %v)", status, periodEnd, test.wantStatus, test.wantEnd)
			}
		})
	}
}

func TestProratedTrialAmount(t *testing.T) {
	tests := []struct {
		name         string
		priceCents   int64
		durationDays int
		trialDays    int
		want         int64
		wantErr      bool
	}{
		{name: "monthly twenty billable days", priceCents: 3000, durationDays: 30, trialDays: 10, want: 2000},
		{name: "rounds to nearest cent", priceCents: 4900, durationDays: 30, trialDays: 10, want: 3267},
		{name: "annual", priceCents: 36500, durationDays: 365, trialDays: 10, want: 35500},
		{name: "minimum one cent", priceCents: 1, durationDays: 30, trialDays: 29, want: 1},
		{name: "trial consumes cycle", priceCents: 3000, durationDays: 30, trialDays: 30, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := proratedTrialAmount(test.priceCents, test.durationDays, test.trialDays)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("proratedTrialAmount() = %d, %v; want %d", got, err, test.want)
			}
		})
	}
}
