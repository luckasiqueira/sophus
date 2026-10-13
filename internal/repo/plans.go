package repo

import (
	"database/sql"
	"sophus/internal/objectcache"
	"strconv"
	"time"
)

func ListPlans(includeInactive bool) ([]Plan, error) {
	var plans []Plan
	err := objectcache.Get("configuration", "plans:"+strconv.FormatBool(includeInactive), time.Minute, &plans, func() error {
		var err error
		plans, err = listPlans(includeInactive)
		return err
	})
	return plans, err
}

func listPlans(includeInactive bool) ([]Plan, error) {
	rows, err := db.Query(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
		"trialDays", "storageLimitGB", "isActive", "createdAt", "updatedAt", "allowAddons"
		FROM plans WHERE $1 OR "isActive" = true ORDER BY "monthlyPriceCents", name`, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plans := []Plan{}
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func GetPlanById(id int) (Plan, error) {
	var plan Plan
	err := objectcache.Get("configuration", "plan:"+strconv.Itoa(id), time.Minute, &plan, func() error {
		var err error
		plan, err = getPlanByID(id)
		return err
	})
	return plan, err
}

func getPlanByID(id int) (Plan, error) {
	return scanPlan(db.QueryRow(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
		"trialDays", "storageLimitGB", "isActive", "createdAt", "updatedAt", "allowAddons" FROM plans WHERE id = $1`, id))
}

func CreatePlan(input CreatePlanInput) (Plan, error) {
	defer invalidateSettingsCache()
	return scanPlan(db.QueryRow(`INSERT INTO plans
		(name, description, "monthlyPriceCents", "annualPriceCents", "priceCents", "durationDays",
			"agentLimit", "connectionLimit", "trialDays", "storageLimitGB", "isActive", "allowAddons")
		VALUES ($1, $2, $3, $4, $3, 30, $5, $6, $7, $8, $9, $10)
		RETURNING id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
			"trialDays", "storageLimitGB", "isActive", "createdAt", "updatedAt", "allowAddons"`, input.Name, input.Description, input.MonthlyPriceCents,
		input.AnnualPriceCents, input.AgentLimit, input.ConnectionLimit, input.TrialDays, input.StorageLimitGB, input.IsActive, input.AllowAddons))
}

func UpdatePlan(id int, input UpdatePlanInput) (bool, error) {
	defer invalidateSettingsCache()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	companyRows, err := tx.Query(`SELECT c.id FROM companies c
		WHERE EXISTS(SELECT 1 FROM subscriptions s WHERE s."companyId" = c.id AND s."planId" = $1 AND s.status IN ('trialing', 'active', 'past_due'))
			OR EXISTS(SELECT 1 FROM subscription_plan_changes change WHERE change."companyId" = c.id AND change."targetPlanId" = $1 AND change.status = 'pending')
		ORDER BY c.id FOR UPDATE OF c`, id)
	if err != nil {
		return false, err
	}
	for companyRows.Next() {
		var companyID int
		if err := companyRows.Scan(&companyID); err != nil {
			companyRows.Close()
			return false, err
		}
	}
	if err := companyRows.Err(); err != nil {
		companyRows.Close()
		return false, err
	}
	if err := companyRows.Close(); err != nil {
		return false, err
	}
	var planID int
	if err := tx.QueryRow(`SELECT id FROM plans WHERE id = $1 FOR UPDATE`, id).Scan(&planID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	var exceedsLimits bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM companies company
		WHERE (EXISTS(SELECT 1 FROM subscriptions s WHERE s."companyId" = company.id AND s."planId" = $1 AND s.status IN ('trialing', 'active', 'past_due'))
			OR EXISTS(SELECT 1 FROM subscription_plan_changes change WHERE change."companyId" = company.id AND change."targetPlanId" = $1 AND change.status = 'pending'))
			AND ((SELECT COUNT(*) FROM agents a WHERE a."companyId" = company.id) > $2 + CASE WHEN $5 THEN COALESCE((SELECT (addons.quantities->>'extraAgents')::integer FROM subscription_addons addons JOIN subscriptions active ON active.id=addons."subscriptionId" WHERE active."companyId"=company.id AND active.status IN ('trialing','active','past_due') LIMIT 1),0) ELSE 0 END
				OR (SELECT COUNT(*) FROM connections c WHERE c."companyId" = company.id) > $3 + CASE WHEN $5 THEN COALESCE((SELECT (addons.quantities->>'extraConnections')::integer FROM subscription_addons addons JOIN subscriptions active ON active.id=addons."subscriptionId" WHERE active."companyId"=company.id AND active.status IN ('trialing','active','past_due') LIMIT 1),0) ELSE 0 END
				OR COALESCE((SELECT su."usedBytes" + su."reservedBytes" FROM company_storage_usage su WHERE su."companyId" = company.id), 0) >= ($4::bigint + CASE WHEN $5 THEN COALESCE((SELECT (addons.quantities->>'extraStorageGB')::integer FROM subscription_addons addons JOIN subscriptions active ON active.id=addons."subscriptionId" WHERE active."companyId"=company.id AND active.status IN ('trialing','active','past_due') LIMIT 1),0) ELSE 0 END) * 1073741824)
	)`, id, input.AgentLimit, input.ConnectionLimit, input.StorageLimitGB, input.AllowAddons).Scan(&exceedsLimits); err != nil {
		return false, err
	}
	if exceedsLimits {
		return false, ErrPlanLimitBelowUsage
	}
	if !input.AllowAddons {
		var contracted bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM subscription_addons a JOIN subscriptions s ON s.id=a."subscriptionId" WHERE s."planId"=$1 AND s.status IN ('trialing','active','past_due') AND (COALESCE((a.quantities->>'extraAgents')::integer,0)>0 OR COALESCE((a.quantities->>'extraConnections')::integer,0)>0 OR COALESCE((a.quantities->>'extraStorageGB')::integer,0)>0 OR COALESCE((a.quantities->>'audioTranscription')::boolean,false)))`, id).Scan(&contracted); err != nil {
			return false, err
		}
		if contracted {
			return false, ErrAddonsNotAllowed
		}
	}
	result, err := tx.Exec(`UPDATE plans SET name = $1, description = $2, "monthlyPriceCents" = $3,
		"annualPriceCents" = $4, "priceCents" = $3, "durationDays" = 30,
		"agentLimit" = $5, "connectionLimit" = $6, "trialDays" = $7, "storageLimitGB" = $8, "isActive" = $9,
		"allowAddons" = $10, "updatedAt" = now() WHERE id = $11`, input.Name, input.Description, input.MonthlyPriceCents, input.AnnualPriceCents,
		input.AgentLimit, input.ConnectionLimit, input.TrialDays, input.StorageLimitGB, input.IsActive, input.AllowAddons, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func scanPlan(scanner rowScanner) (Plan, error) {
	var plan Plan
	err := scanner.Scan(&plan.Id, &plan.Name, &plan.Description, &plan.MonthlyPriceCents, &plan.AnnualPriceCents,
		&plan.AgentLimit, &plan.ConnectionLimit, &plan.TrialDays, &plan.StorageLimitGB, &plan.IsActive, &plan.CreatedAt, &plan.UpdatedAt, &plan.AllowAddons)
	return plan, err
}

func BillingCycleTerms(plan Plan, cycle string) (int64, int, error) {
	switch cycle {
	case BillingCycleMonthly:
		return plan.MonthlyPriceCents, 30, nil
	case BillingCycleAnnual:
		return plan.AnnualPriceCents, 365, nil
	default:
		return 0, 0, ErrInvalidBillingCycle
	}
}
