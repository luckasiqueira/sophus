package controllers

import (
	"testing"

	"sophus/internal/repo"
)

func TestNormalizeAndValidatePlanTrialDays(t *testing.T) {
	for _, trialDays := range []int{0, 10, 90} {
		input := repo.PlanInput{Name: "Plano", MonthlyPriceCents: 1000, AnnualPriceCents: 10000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: trialDays}
		if message := normalizeAndValidatePlan(&input); message != "" {
			t.Fatalf("trialDays %d rejected: %s", trialDays, message)
		}
	}
	for _, trialDays := range []int{-1, 91} {
		input := repo.PlanInput{Name: "Plano", MonthlyPriceCents: 1000, AnnualPriceCents: 10000, AgentLimit: 1, ConnectionLimit: 1, TrialDays: trialDays}
		if message := normalizeAndValidatePlan(&input); message == "" {
			t.Fatalf("trialDays %d accepted", trialDays)
		}
	}
}
