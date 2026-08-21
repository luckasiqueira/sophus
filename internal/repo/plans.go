package repo

import "database/sql"

func ListPlans(includeInactive bool) ([]Plan, error) {
	rows, err := db.Query(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
		"isActive", "createdAt", "updatedAt"
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
	return scanPlan(db.QueryRow(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
		"isActive", "createdAt", "updatedAt" FROM plans WHERE id = $1`, id))
}

func CreatePlan(input CreatePlanInput) (Plan, error) {
	return scanPlan(db.QueryRow(`INSERT INTO plans
		(name, description, "monthlyPriceCents", "annualPriceCents", "priceCents", "durationDays",
			"agentLimit", "connectionLimit", "isActive")
		VALUES ($1, $2, $3, $4, $3, 30, $5, $6, $7)
		RETURNING id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit", "connectionLimit",
			"isActive", "createdAt", "updatedAt"`, input.Name, input.Description, input.MonthlyPriceCents,
		input.AnnualPriceCents, input.AgentLimit, input.ConnectionLimit, input.IsActive))
}

func UpdatePlan(id int, input UpdatePlanInput) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	companyRows, err := tx.Query(`SELECT c.id
		FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id
		WHERE s."planId" = $1 AND s.status IN ('trialing', 'active', 'past_due')
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
		SELECT 1 FROM subscriptions s
		WHERE s."planId" = $1 AND s.status IN ('trialing', 'active', 'past_due')
			AND ((SELECT COUNT(*) FROM agents a WHERE a."companyId" = s."companyId") > $2
				OR (SELECT COUNT(*) FROM connections c WHERE c."companyId" = s."companyId") > $3)
	)`, id, input.AgentLimit, input.ConnectionLimit).Scan(&exceedsLimits); err != nil {
		return false, err
	}
	if exceedsLimits {
		return false, ErrPlanLimitBelowUsage
	}
	result, err := tx.Exec(`UPDATE plans SET name = $1, description = $2, "monthlyPriceCents" = $3,
		"annualPriceCents" = $4, "priceCents" = $3, "durationDays" = 30,
		"agentLimit" = $5, "connectionLimit" = $6, "isActive" = $7, "updatedAt" = now()
		WHERE id = $8`, input.Name, input.Description, input.MonthlyPriceCents, input.AnnualPriceCents,
		input.AgentLimit, input.ConnectionLimit, input.IsActive, id)
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
		&plan.AgentLimit, &plan.ConnectionLimit, &plan.IsActive, &plan.CreatedAt, &plan.UpdatedAt)
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
