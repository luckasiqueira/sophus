package controllers

import (
	"testing"

	"sophus/internal/repo"
)

func TestNormalizeAndValidatePlanTrialDays(t *testing.T) {
	for _, trialDays := range []int{0, 10, 29} {
		input := repo.PlanInput{Name: "Plano", MonthlyPriceCents: 1000, AnnualPriceCents: 10000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: trialDays, StorageLimitGB: 5}
		if message := normalizeAndValidatePlan(&input); message != "" {
			t.Fatalf("trialDays %d rejected: %s", trialDays, message)
		}
	}
	annualOnly := repo.PlanInput{Name: "Plano anual", AnnualPriceCents: 10000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: 90, StorageLimitGB: 5}
	if message := normalizeAndValidatePlan(&annualOnly); message != "" {
		t.Fatalf("annual-only trial rejected: %s", message)
	}
	monthlyTooLong := repo.PlanInput{Name: "Plano mensal", MonthlyPriceCents: 1000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: 30, StorageLimitGB: 5}
	if message := normalizeAndValidatePlan(&monthlyTooLong); message == "" {
		t.Fatal("monthly trial equal to cycle was accepted")
	}
	for _, trialDays := range []int{-1, 91} {
		input := repo.PlanInput{Name: "Plano", MonthlyPriceCents: 1000, AnnualPriceCents: 10000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: trialDays, StorageLimitGB: 5}
		if message := normalizeAndValidatePlan(&input); message == "" {
			t.Fatalf("trialDays %d accepted", trialDays)
		}
	}
}

func TestNormalizeAndValidatePlanStorageLimit(t *testing.T) {
	for _, limit := range []int{0, 100001} {
		input := repo.PlanInput{Name: "Plano", AgentLimit: 1, ConnectionLimit: 1, StorageLimitGB: limit}
		if message := normalizeAndValidatePlan(&input); message == "" {
			t.Fatalf("storageLimitGB %d accepted", limit)
		}
	}
}
