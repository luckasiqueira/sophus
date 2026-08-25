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
