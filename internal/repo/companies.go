package repo

import (
	"database/sql"
	"sophus/internal/objectcache"
	"strconv"
	"time"
)

func ListCompanies() ([]CompanySummary, error) {
	if err := refreshOverdueSubscriptions(0); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT c.id, c.name, c.email, c."isActive", c.timezone, c."createdAt", c."updatedAt",
		s.id, s."planId", p.name, s.status, s."priceCents", s."billingCycle", s."currentPeriodStart", s."currentPeriodEnd", s."redemptionEndsAt"
		FROM companies c
		LEFT JOIN LATERAL (
			SELECT id, "planId", status, "priceCents", "billingCycle", "currentPeriodStart", "currentPeriodEnd", "redemptionEndsAt"
			FROM subscriptions
			WHERE "companyId" = c.id
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
		var periodStart, periodEnd, redemptionEnd sql.NullTime
		if err := rows.Scan(&company.Id, &company.Name, &company.Email, &company.IsActive, &company.Timezone,
			&company.CreatedAt, &company.UpdatedAt, &subscriptionID, &planID, &planName, &status, &priceCents, &billingCycle,
			&periodStart, &periodEnd, &redemptionEnd); err != nil {
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
		company.RedemptionEndsAt = nullTimePointer(redemptionEnd)
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
		"connectionLimit", "trialDays", "storageLimitGB", "isActive", "createdAt", "updatedAt", "allowAddons"
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

	var originalAdminID int
	if err := tx.QueryRow(`INSERT INTO agents
		(name, email, password, role, "isActive", "companyId", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, true, $5, now(), now()) RETURNING id`, input.AdminName, input.AdminEmail,
		input.AdminPasswordHash, RoleAdmin, company.Id).Scan(&originalAdminID); err != nil {
		return Company{}, err
	}
	if _, err := tx.Exec(`UPDATE companies SET "originalAdminId" = $1 WHERE id = $2`, originalAdminID, company.Id); err != nil {
		return Company{}, err
	}

	now := time.Now().UTC()
	status, periodEnd := initialSubscriptionTerms(priceCents, durationDays, plan.TrialDays, now)
	var subscriptionID int
	if err := tx.QueryRow(`INSERT INTO subscriptions
		("companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt", "currentPeriodStart", "currentPeriodEnd", "basePeriodEnd")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $8) RETURNING id`,
		company.Id, plan.Id, status, priceCents, durationDays, input.BillingCycle, now, periodEnd).Scan(&subscriptionID); err != nil {
		return Company{}, err
	}
	if status == SubscriptionStatusTrialing {
		amountCents, err := proratedTrialAmount(priceCents, durationDays, plan.TrialDays)
		if err != nil {
			return Company{}, err
		}
		if renewalInvoiceDue(periodEnd, now) {
			var provider string
			if err := tx.QueryRow(`SELECT "paymentProvider" FROM platform_settings WHERE id = 1`).Scan(&provider); err != nil {
				return Company{}, err
			}
			var paymentID int
			if err := tx.QueryRow(`INSERT INTO payments
			("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
			VALUES ($1, $2, 'pending', $3, $4, $5, 0) RETURNING id`,
				company.Id, subscriptionID, amountCents, periodEnd, provider).Scan(&paymentID); err != nil {
				return Company{}, err
			}
			if _, err := tx.Exec(`INSERT INTO payment_fixed_entitlements ("paymentId", "periodEnd") VALUES ($1, $2)`,
				paymentID, now.AddDate(0, 0, durationDays)); err != nil {
				return Company{}, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return Company{}, err
	}
	return company, nil
}

func proratedTrialAmount(priceCents int64, durationDays, trialDays int) (int64, error) {
	if priceCents <= 0 || durationDays <= 0 || trialDays <= 0 || trialDays >= durationDays {
		return 0, ErrInvalidTrialDays
	}
	billableDays := int64(durationDays - trialDays)
	duration := int64(durationDays)
	amountCents := (priceCents/duration)*billableDays + ((priceCents%duration)*billableDays+duration/2)/duration
	if amountCents < 1 {
		amountCents = 1
	}
	return amountCents, nil
}

func initialSubscriptionTerms(priceCents int64, durationDays, trialDays int, now time.Time) (string, time.Time) {
	if priceCents == 0 {
		return SubscriptionStatusActive, now.AddDate(0, 0, durationDays)
	}
	if trialDays > 0 {
		return SubscriptionStatusTrialing, now.AddDate(0, 0, trialDays)
	}
	return SubscriptionStatusPastDue, now.Add(time.Microsecond)
}

func UpdateCompany(id int, input UpdateCompanyInput) (bool, error) {
	defer invalidateSettingsCache()
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
	defer invalidateSettingsCache()
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
	var snapshot cachedCompanyAccess
	err := objectcache.Get("access", "company-v3:"+strconv.Itoa(id), 10*time.Second, &snapshot, func() error {
		var err error
		snapshot, err = companyAccessSnapshot(id)
		return err
	})
	return snapshot.valid(time.Now()), err
}

type cachedCompanyAccess struct {
	Allowed    bool       `json:"allowed"`
	ValidUntil *time.Time `json:"validUntil"`
}

func (access cachedCompanyAccess) valid(now time.Time) bool {
	return access.Allowed && access.ValidUntil != nil && !now.After(*access.ValidUntil)
}

func companyAccessSnapshot(id int) (cachedCompanyAccess, error) {
	var snapshot cachedCompanyAccess
	var deadline sql.NullTime
	err := db.QueryRow(`SELECT COUNT(*) > 0, MAX(s."currentPeriodEnd" + (ps."subscriptionGraceDays" * interval '1 day'))
		FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id
		JOIN plans p ON p.id = s."planId"
		JOIN subscription_effective_limits limits ON limits."subscriptionId"=s.id
		LEFT JOIN company_storage_usage su ON su."companyId" = c.id
		CROSS JOIN platform_settings ps
		WHERE c.id = $1
			AND ps.id = 1
			AND c."isActive" = true
			AND s.status IN ('trialing', 'active', 'past_due')
			AND (
				(s."priceCents" = 0 AND s."currentPeriodEnd" >= now())
				OR (s.status IN ('trialing', 'active') AND s."currentPeriodEnd" + (ps."subscriptionGraceDays" * interval '1 day') >= now())
				OR (s.status = 'past_due' AND s."currentPeriodEnd" + (ps."subscriptionGraceDays" * interval '1 day') >= now())
			)
			AND COALESCE(su."usedBytes", 0) < limits."storageLimitGB"::bigint * 1073741824
	`, id).Scan(&snapshot.Allowed, &deadline)
	if deadline.Valid {
		snapshot.ValidUntil = &deadline.Time
	}
	return snapshot, err
}

func refreshOverdueSubscriptions(companyID int) error {
	changed := false
	defer func() {
		if changed {
			invalidateAccessCache()
		}
	}()
	execute := func(query string, args ...any) (sql.Result, error) {
		result, err := db.Exec(query, args...)
		if err == nil {
			if rows, rowErr := result.RowsAffected(); rowErr == nil && rows > 0 {
				changed = true
			}
		}
		return result, err
	}
	if _, err := execute(`UPDATE subscriptions s SET
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
	if _, err := execute(`UPDATE subscriptions SET status = 'past_due', "updatedAt" = now()
		WHERE status IN ('trialing', 'active') AND "priceCents" > 0 AND "currentPeriodEnd" < now()
			AND ($1 = 0 OR "companyId" = $1)`, companyID); err != nil {
		return err
	}
	if _, err := execute(`UPDATE subscriptions subscription SET status = 'active', "updatedAt" = now()
		FROM payments payment
		JOIN payment_fixed_entitlements fixed ON fixed."paymentId" = payment.id
		WHERE payment."subscriptionId" = subscription.id AND payment.status = 'paid' AND payment."dueDate" <= now()
			AND subscription.status = 'trialing' AND ($1 = 0 OR subscription."companyId" = $1)`, companyID); err != nil {
		return err
	}
	_, err := execute(`UPDATE subscriptions subscription SET status = 'expired', "updatedAt" = now()
		FROM platform_settings settings
		WHERE subscription.status = 'past_due' AND subscription."redemptionEndsAt" IS NOT NULL
			AND subscription."redemptionEndsAt" < now()
			AND subscription."currentPeriodEnd" + (settings."subscriptionGraceDays" * interval '1 day') < now()
			AND settings.id = 1 AND ($1 = 0 OR subscription."companyId" = $1)`, companyID)
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
