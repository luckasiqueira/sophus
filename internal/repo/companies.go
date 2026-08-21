package repo

import (
	"database/sql"
	"time"
)

func ListCompanies() ([]CompanySummary, error) {
	if err := refreshOverdueSubscriptions(0); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT c.id, c.name, c.email, c."isActive", c.timezone, c."createdAt", c."updatedAt",
		s.id, s."planId", p.name, s.status, s."priceCents", s."billingCycle", s."currentPeriodStart", s."currentPeriodEnd"
		FROM companies c
		LEFT JOIN LATERAL (
			SELECT id, "planId", status, "priceCents", "billingCycle", "currentPeriodStart", "currentPeriodEnd"
			FROM subscriptions
			WHERE "companyId" = c.id AND status IN ('trialing', 'active', 'past_due')
			ORDER BY "createdAt" DESC LIMIT 1
		) s ON true
		LEFT JOIN plans p ON p.id = s."planId"
		ORDER BY c.name, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	companies := []CompanySummary{}
	for rows.Next() {
		var company CompanySummary
		var subscriptionID, planID sql.NullInt64
		var planName, status, billingCycle sql.NullString
		var priceCents sql.NullInt64
		var periodStart, periodEnd sql.NullTime
		if err := rows.Scan(&company.Id, &company.Name, &company.Email, &company.IsActive, &company.Timezone,
			&company.CreatedAt, &company.UpdatedAt, &subscriptionID, &planID, &planName, &status, &priceCents, &billingCycle,
			&periodStart, &periodEnd); err != nil {
			return nil, err
		}
		company.CurrentSubscriptionId = nullIntPointer(subscriptionID)
		company.CurrentPlanId = nullIntPointer(planID)
		company.CurrentPlanName = nullStringPointer(planName)
		company.SubscriptionStatus = nullStringPointer(status)
		company.PriceCents = nullInt64Pointer(priceCents)
		company.BillingCycle = nullStringPointer(billingCycle)
		company.CurrentPeriodStart = nullTimePointer(periodStart)
		company.CurrentPeriodEnd = nullTimePointer(periodEnd)
		companies = append(companies, company)
	}
	return companies, rows.Err()
}

func GetCompanyById(id int) (Company, error) {
	return scanCompany(db.QueryRow(`SELECT id, name, email, "isActive", timezone, "createdAt", "updatedAt"
		FROM companies WHERE id = $1`, id))
}

func CreateCompanyWithAdmin(input CreateCompanyWithAdminInput) (Company, error) {
	tx, err := db.Begin()
	if err != nil {
		return Company{}, err
	}
	defer tx.Rollback()

	plan, err := scanPlan(tx.QueryRow(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit",
		"connectionLimit", "isActive", "createdAt", "updatedAt"
		FROM plans WHERE id = $1 AND "isActive" = true FOR SHARE`, input.PlanId))
	if err != nil {
		return Company{}, err
	}
	priceCents, durationDays, err := BillingCycleTerms(plan, input.BillingCycle)
	if err != nil {
		return Company{}, err
	}

	company, err := scanCompany(tx.QueryRow(`INSERT INTO companies (name, email, "isActive", timezone)
		SELECT $1, $2, true, COALESCE(NULLIF($3, ''), ps."defaultTimezone")
		FROM platform_settings ps WHERE ps.id = 1
		RETURNING id, name, email, "isActive", timezone, "createdAt", "updatedAt"`,
		input.Name, input.Email, input.Timezone))
	if err != nil {
		return Company{}, err
	}

	if err := lockAgentEmail(tx, input.AdminEmail, 0); err != nil {
		return Company{}, err
	}

	if _, err := tx.Exec(`INSERT INTO agents
		(name, email, password, role, "isActive", "companyId", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, true, $5, now(), now())`, input.AdminName, input.AdminEmail,
		input.AdminPasswordHash, RoleAdmin, company.Id); err != nil {
		return Company{}, err
	}

	status := SubscriptionStatusActive
	periodEndExpression := "now() + ($5 * interval '1 day')"
	baseEndExpression := periodEndExpression
	if priceCents > 0 {
		status = SubscriptionStatusPastDue
		periodEndExpression = "now() + interval '1 microsecond'"
		baseEndExpression = periodEndExpression
	}
	if _, err := tx.Exec(`INSERT INTO subscriptions
		("companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt", "currentPeriodStart", "currentPeriodEnd", "basePeriodEnd")
		VALUES ($1, $2, $3, $4, $5, $6, now(), now(), `+periodEndExpression+`, `+baseEndExpression+`)`,
		company.Id, plan.Id, status, priceCents, durationDays, input.BillingCycle); err != nil {
		return Company{}, err
	}

	if err := tx.Commit(); err != nil {
		return Company{}, err
	}
	return company, nil
}

func UpdateCompany(id int, input UpdateCompanyInput) (bool, error) {
	result, err := db.Exec(`UPDATE companies SET name = $1, email = $2,
		timezone = COALESCE(NULLIF($3, ''), timezone), "updatedAt" = now() WHERE id = $4`,
		input.Name, input.Email, input.Timezone, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func SetCompanyActive(id int, active bool) (bool, error) {
	result, err := db.Exec(`UPDATE companies SET "isActive" = $1, "updatedAt" = now() WHERE id = $2`, active, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func IsCompanyActive(id int) (bool, error) {
	var active bool
	err := db.QueryRow(`SELECT "isActive" FROM companies WHERE id = $1`, id).Scan(&active)
	return active, err
}

func CompanyHasAccess(id int) (bool, error) {
	if err := refreshOverdueSubscriptions(id); err != nil {
		return false, err
	}
	var allowed bool
	err := db.QueryRow(`SELECT EXISTS(
		SELECT 1
		FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id
		CROSS JOIN platform_settings ps
		WHERE c.id = $1
			AND ps.id = 1
			AND c."isActive" = true
			AND s.status IN ('trialing', 'active', 'past_due')
			AND (s."priceCents" = 0 OR s."currentPeriodEnd" + (ps."subscriptionGraceDays" * interval '1 day') >= now())
	)`, id).Scan(&allowed)
	return allowed, err
}

func refreshOverdueSubscriptions(companyID int) error {
	if _, err := db.Exec(`UPDATE subscriptions s SET
		status = 'active',
		"currentPeriodStart" = now(),
		"currentPeriodEnd" = now() + (s."durationDays" * interval '1 day'),
		"basePeriodEnd" = now() + (s."durationDays" * interval '1 day'),
		"updatedAt" = now()
		WHERE s.status IN ('trialing', 'active', 'past_due')
			AND s."priceCents" = 0
			AND s."currentPeriodEnd" < now()
			AND ($1 = 0 OR s."companyId" = $1)`, companyID); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE subscriptions SET status = 'past_due', "updatedAt" = now()
		WHERE status IN ('trialing', 'active') AND "priceCents" > 0 AND "currentPeriodEnd" < now()
			AND ($1 = 0 OR "companyId" = $1)`, companyID)
	return err
}

func scanCompany(scanner rowScanner) (Company, error) {
	var company Company
	err := scanner.Scan(&company.Id, &company.Name, &company.Email, &company.IsActive, &company.Timezone,
		&company.CreatedAt, &company.UpdatedAt)
	return company, err
}

func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
