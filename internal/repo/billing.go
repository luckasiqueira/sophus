package repo

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	ErrUnknownMercadoPagoStatus = errors.New("unknown Mercado Pago payment status")
	ErrUnknownAbacatePayStatus  = errors.New("unknown AbacatePay payment status")
	ErrProviderPaymentMismatch  = errors.New("provider payment does not match the internal payment")
	ErrInvalidPaymentStatus     = errors.New("invalid internal payment status")
	ErrOpenPayments             = errors.New("subscription has open payments")
)

func GetCurrentSubscription(companyID int) (Subscription, error) {
	if err := refreshOverdueSubscriptions(companyID); err != nil {
		return Subscription{}, err
	}
	return scanSubscription(db.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions
		WHERE "companyId" = $1 AND status IN ('trialing', 'active', 'past_due')
		ORDER BY "createdAt" DESC LIMIT 1`, companyID))
}

func ChangeCompanyPlan(companyID, planID int, billingCycle string) (Subscription, error) {
	if !IsValidBillingCycle(billingCycle) {
		return Subscription{}, ErrInvalidBillingCycle
	}
	tx, err := db.Begin()
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()

	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Subscription{}, err
	}
	plan, err := scanPlan(tx.QueryRow(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit",
		"connectionLimit", "isActive", "createdAt", "updatedAt"
		FROM plans WHERE id = $1 AND "isActive" = true FOR SHARE`, planID))
	if err != nil {
		return Subscription{}, err
	}
	priceCents, durationDays, err := BillingCycleTerms(plan, billingCycle)
	if err != nil {
		return Subscription{}, err
	}
	current, err := scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions
		WHERE "companyId" = $1 AND status IN ('trialing', 'active', 'past_due')
		FOR UPDATE`, companyID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, err
	}
	if err == nil && current.PlanId == plan.Id && current.BillingCycle == billingCycle {
		if err := tx.Commit(); err != nil {
			return Subscription{}, err
		}
		return current, nil
	}
	var agentCount, connectionCount int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM agents WHERE "companyId" = $1),
		(SELECT COUNT(*) FROM connections WHERE "companyId" = $1)`, companyID).Scan(&agentCount, &connectionCount); err != nil {
		return Subscription{}, err
	}
	if agentCount > plan.AgentLimit || connectionCount > plan.ConnectionLimit {
		return Subscription{}, ErrPlanLimitBelowUsage
	}
	if errors.Is(err, sql.ErrNoRows) {
		current = Subscription{}
	}

	if current.Id != 0 {
		if _, err := tx.Exec(`UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
			WHERE "subscriptionId" = $1 AND status = 'pending' AND (
				("providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL
					AND "checkoutKey" IS NULL AND "checkoutCreating" = false)
				OR (provider = 'mercado_pago' AND "dueDate" <= now())
			)`, current.Id); err != nil {
			return Subscription{}, err
		}
		var hasOpenPayments bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments
			WHERE "subscriptionId" = $1 AND status IN ('pending', 'processing'))`, current.Id).Scan(&hasOpenPayments); err != nil {
			return Subscription{}, err
		}
		if hasOpenPayments {
			return Subscription{}, ErrOpenPayments
		}
		result, err := tx.Exec(`UPDATE subscriptions SET status = $1, "canceledAt" = now(), "updatedAt" = now()
			WHERE id = $2 AND "companyId" = $3`, SubscriptionStatusCanceled, current.Id, companyID)
		if err != nil {
			return Subscription{}, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return Subscription{}, err
		}
		if rows != 1 {
			return Subscription{}, sql.ErrNoRows
		}
	}

	periodStart := time.Now().UTC()
	periodEnd := periodStart.AddDate(0, 0, durationDays)
	basePeriodEnd := periodEnd
	status := SubscriptionStatusActive
	var previousSubscriptionID any
	if current.Id != 0 {
		periodStart = current.CurrentPeriodStart
		periodEnd = current.CurrentPeriodEnd
		basePeriodEnd = periodEnd
		status = current.Status
		previousSubscriptionID = current.Id
	}
	if priceCents > 0 && (current.Id == 0 || current.PriceCents == 0) {
		periodStart = time.Now().UTC()
		periodEnd = periodStart.Add(time.Microsecond)
		basePeriodEnd = periodEnd
		status = SubscriptionStatusPastDue
	}
	subscription, err := scanSubscription(tx.QueryRow(`INSERT INTO subscriptions
		("companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
			"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "previousSubscriptionId")
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8, $9, $10)
		RETURNING id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt", "currentPeriodStart",
			"currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"`, companyID, plan.Id,
		status, priceCents, durationDays, billingCycle, periodStart, periodEnd, basePeriodEnd, previousSubscriptionID))
	if err != nil {
		return Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, err
	}
	return subscription, nil
}

func GetPaymentsByCompany(companyID int) ([]Payment, error) {
	rows, err := db.Query(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE "companyId" = $1 ORDER BY "createdAt" DESC, id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	payments := []Payment{}
	for rows.Next() {
		payment, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}
	return payments, rows.Err()
}

func GetPaymentByIdAndCompany(id, companyID int) (Payment, error) {
	return scanPayment(db.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1 AND "companyId" = $2`, id, companyID))
}

func GetPaymentById(id int) (Payment, error) {
	return scanPayment(db.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1`, id))
}

func CreatePaymentForCurrentSubscription(companyID int, provider string) (Payment, bool, error) {
	if !IsValidPaymentProvider(provider) {
		return Payment{}, false, ErrInvalidPaymentProvider
	}
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, false, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, false, err
	}

	subscription, err := scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions
		WHERE "companyId" = $1 AND status IN ('trialing', 'active', 'past_due')
		FOR UPDATE`, companyID))
	if err != nil {
		return Payment{}, false, err
	}
	if _, err := tx.Exec(`UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
		WHERE "subscriptionId" = $1 AND provider = 'mercado_pago' AND status = 'pending' AND "dueDate" <= now()`, subscription.Id); err != nil {
		return Payment{}, false, err
	}

	payment, err := scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments
		WHERE "companyId" = $1 AND "subscriptionId" = $2 AND status IN ('pending', 'processing')
			AND "amountCents" = $3
			AND ("dueDate" > now() OR (provider = 'abacate_pay' AND status = 'pending'))
		ORDER BY "createdAt" DESC LIMIT 1 FOR UPDATE`, companyID, subscription.Id,
		subscription.PriceCents))
	if err == nil {
		if payment.Provider != provider && payment.ProviderPaymentId == nil && payment.ProviderPreferenceId == nil &&
			payment.CheckoutURL == nil && payment.CheckoutKey == nil {
			result, updateErr := tx.Exec(`UPDATE payments SET status = 'canceled', "updatedAt" = now()
				WHERE id = $1 AND status IN ('pending', 'processing') AND "checkoutCreating" = false
					AND "providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL
					AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL`, payment.Id)
			if updateErr != nil {
				return Payment{}, false, updateErr
			}
			updated, updateErr := result.RowsAffected()
			if updateErr != nil {
				return Payment{}, false, updateErr
			}
			if updated == 1 {
				err = sql.ErrNoRows
			}
		}
	}
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Payment{}, false, err
		}
		return payment, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Payment{}, false, err
	}

	dueDate := paymentDueDate(subscription.CurrentPeriodEnd, time.Now().UTC())
	payment, err = scanPayment(tx.QueryRow(`INSERT INTO payments
		("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
		VALUES ($1, $2, $3, $4, $5, $6, (SELECT "periodRevision" FROM subscriptions WHERE id = $2))
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		companyID, subscription.Id, PaymentStatusPending, subscription.PriceCents, dueDate, provider))
	if err != nil {
		return Payment{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, false, err
	}
	return payment, true, nil
}

func SetPaymentCheckout(paymentID int, checkoutLease string, providerPaymentID, providerPreferenceID, checkoutURL *string) (Payment, error) {
	return scanPayment(db.QueryRow(`UPDATE payments SET
		"providerPaymentId" = COALESCE(NULLIF($1, ''), "providerPaymentId"),
		"providerPreferenceId" = COALESCE(NULLIF($2, ''), "providerPreferenceId"),
		"checkoutURL" = COALESCE(NULLIF($3, ''), "checkoutURL"),
		"checkoutCreating" = false,
		"checkoutLease" = NULL,
		"updatedAt" = now()
		WHERE id = $4 AND "checkoutCreating" = true AND "checkoutLease" = $5
			AND (NULLIF($1, '') IS NULL OR "providerPaymentId" IS NULL OR "providerPaymentId" = $1)
			AND (NULLIF($2, '') IS NULL OR "providerPreferenceId" IS NULL OR "providerPreferenceId" = $2)
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		providerPaymentID, providerPreferenceID, checkoutURL, paymentID, checkoutLease))
}

func ClaimPaymentCheckout(paymentID int, newCheckoutKey string) (string, bool, bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", false, false, err
	}
	defer tx.Rollback()
	var existingKey sql.NullString
	err = tx.QueryRow(`SELECT "checkoutKey" FROM payments
		WHERE id = $1 AND status = 'pending'
			AND ("checkoutCreating" = false OR "updatedAt" < now() - interval '5 minutes')
			AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL
		FOR UPDATE`, paymentID).Scan(&existingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	checkoutKey := newCheckoutKey
	if existingKey.Valid && existingKey.String != "" {
		checkoutKey = existingKey.String
	}
	if _, err := tx.Exec(`UPDATE payments SET "checkoutCreating" = true, "checkoutKey" = $1,
		"checkoutLease" = $2, "updatedAt" = now()
		WHERE id = $3`, checkoutKey, newCheckoutKey, paymentID); err != nil {
		return "", false, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, false, err
	}
	return checkoutKey, existingKey.Valid, true, nil
}

func ReleasePaymentCheckout(paymentID int, checkoutLease string) error {
	_, err := db.Exec(`UPDATE payments SET "checkoutCreating" = false, "checkoutLease" = NULL,
		"checkoutKey" = CASE
			WHEN "providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL THEN NULL
			ELSE "checkoutKey" END,
		"updatedAt" = now()
		WHERE id = $1 AND "checkoutCreating" = true AND "checkoutLease" = $2`, paymentID, checkoutLease)
	return err
}

func ApplyMercadoPagoPayment(paymentID int, providerPaymentID, providerStatus string, paidAt *time.Time) (Payment, bool, error) {
	mappedStatus, err := MapMercadoPagoPaymentStatus(providerStatus)
	if err != nil {
		return Payment{}, false, err
	}
	return ApplyProviderPayment(paymentID, PaymentProviderMercadoPago, providerPaymentID, mappedStatus, paidAt, nil)
}

func ApplyProviderPayment(paymentID int, provider, providerPaymentID, mappedInternalStatus string, paidAt *time.Time, receiptURL *string) (Payment, bool, error) {
	if err := validateProviderPaymentUpdate(provider, mappedInternalStatus); err != nil {
		return Payment{}, false, err
	}

	tx, err := db.Begin()
	if err != nil {
		return Payment{}, false, err
	}
	defer tx.Rollback()
	payment, firstPaidTransition, err := applyProviderPayment(tx, paymentID, provider, providerPaymentID,
		mappedInternalStatus, paidAt, receiptURL)
	if err != nil {
		return Payment{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, false, err
	}
	return payment, firstPaidTransition, nil
}

func ApplyProviderPaymentEvent(eventID string, paymentID int, provider, providerPaymentID, mappedInternalStatus string, paidAt *time.Time, receiptURL *string) (Payment, bool, error) {
	if err := validateProviderPaymentUpdate(provider, mappedInternalStatus); err != nil {
		return Payment{}, false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, false, err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`INSERT INTO provider_webhook_events (provider, "eventId")
		VALUES ($1, $2) ON CONFLICT (provider, "eventId") DO NOTHING`, provider, eventID)
	if err != nil {
		return Payment{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Payment{}, false, err
	}
	if inserted == 0 {
		payment, err := scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
			FROM payments WHERE id = $1 AND provider = $2`, paymentID, provider))
		if errors.Is(err, sql.ErrNoRows) {
			return Payment{}, false, nil
		}
		if err != nil {
			return Payment{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return Payment{}, false, err
		}
		return payment, false, nil
	}

	payment, firstPaidTransition, err := applyProviderPayment(tx, paymentID, provider, providerPaymentID,
		mappedInternalStatus, paidAt, receiptURL)
	if err != nil {
		return Payment{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, false, err
	}
	return payment, firstPaidTransition, nil
}

func applyProviderPayment(tx *sql.Tx, paymentID int, provider, providerPaymentID, mappedInternalStatus string, paidAt *time.Time, receiptURL *string) (Payment, bool, error) {
	var subscriptionID int
	var companyID int
	if err := tx.QueryRow(`SELECT "subscriptionId", "companyId" FROM payments
		WHERE id = $1 AND provider = $2`, paymentID, provider).Scan(&subscriptionID, &companyID); err != nil {
		return Payment{}, false, err
	}
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, false, err
	}
	var basePeriodEnd time.Time
	var currentPeriodEnd time.Time
	var durationDays int
	var periodRevision int
	if err := tx.QueryRow(`SELECT "basePeriodEnd", "currentPeriodEnd", "durationDays", "periodRevision"
		FROM subscriptions WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, subscriptionID, companyID).Scan(
		&basePeriodEnd, &currentPeriodEnd, &durationDays, &periodRevision); err != nil {
		return Payment{}, false, err
	}

	payment, err := scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1 FOR UPDATE`, paymentID))
	if err != nil {
		return Payment{}, false, err
	}
	if payment.ProviderPaymentId != nil && *payment.ProviderPaymentId != providerPaymentID {
		return Payment{}, false, ErrProviderPaymentMismatch
	}

	firstPaidTransition := mappedInternalStatus == PaymentStatusPaid && payment.PaidAt == nil && payment.Status != PaymentStatusRefunded
	firstRefundTransition := mappedInternalStatus == PaymentStatusRefunded && payment.Status == PaymentStatusPaid
	nextStatus := nextPaymentStatus(payment.Status, mappedInternalStatus)
	effectivePaidAt := payment.PaidAt
	if firstPaidTransition {
		if paidAt != nil {
			value := *paidAt
			effectivePaidAt = &value
		} else {
			value := time.Now().UTC()
			effectivePaidAt = &value
		}
	}
	payment, err = scanPayment(tx.QueryRow(`UPDATE payments SET
		"providerPaymentId" = COALESCE(NULLIF($1, ''), "providerPaymentId"),
		status = $2,
		"paidAt" = $3,
		"receiptURL" = COALESCE(NULLIF($4, ''), "receiptURL"),
		"entitlementRevision" = CASE WHEN $5 THEN $6 ELSE "entitlementRevision" END,
		"updatedAt" = now()
		WHERE id = $7
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		providerPaymentID, nextStatus, effectivePaidAt, receiptURL, firstPaidTransition, periodRevision, paymentID))
	if err != nil {
		return Payment{}, false, err
	}

	if firstPaidTransition || firstRefundTransition {
		if err := recomputeSubscriptionPeriod(tx, subscriptionID, basePeriodEnd, currentPeriodEnd, durationDays,
			periodRevision); err != nil {
			return Payment{}, false, err
		}
	}

	return payment, firstPaidTransition, nil
}

func validateProviderPaymentUpdate(provider, mappedInternalStatus string) error {
	if !IsValidPaymentProvider(provider) {
		return ErrInvalidPaymentProvider
	}
	if !isValidPaymentStatus(mappedInternalStatus) {
		return ErrInvalidPaymentStatus
	}
	return nil
}

func recomputeSubscriptionPeriod(tx *sql.Tx, subscriptionID int, basePeriodEnd, previousPeriodEnd time.Time, durationDays, periodRevision int) error {
	paidDates, err := subscriptionPaidDates(tx, subscriptionID, periodRevision)
	if err != nil {
		return err
	}
	periodEnd := recomputePeriodEnd(basePeriodEnd, paidDates, durationDays)
	result, err := updateSubscriptionPeriod(tx, subscriptionID, basePeriodEnd, periodEnd)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}

	deltaMicroseconds := periodEnd.Sub(previousPeriodEnd).Microseconds()
	if deltaMicroseconds == 0 {
		return nil
	}
	return recomputeSuccessorPeriods(tx, subscriptionID, time.Duration(deltaMicroseconds)*time.Microsecond)
}

func recomputeSuccessorPeriods(tx *sql.Tx, previousSubscriptionID int, inheritedDelta time.Duration) error {
	type successorPeriod struct {
		id               int
		basePeriodEnd    time.Time
		currentPeriodEnd time.Time
		durationDays     int
		periodRevision   int
	}
	rows, err := tx.Query(`SELECT id, "basePeriodEnd", "currentPeriodEnd", "durationDays", "periodRevision"
		FROM subscriptions WHERE "previousSubscriptionId" = $1 ORDER BY id FOR UPDATE`, previousSubscriptionID)
	if err != nil {
		return err
	}
	successors := []successorPeriod{}
	for rows.Next() {
		var successor successorPeriod
		if err := rows.Scan(&successor.id, &successor.basePeriodEnd, &successor.currentPeriodEnd,
			&successor.durationDays, &successor.periodRevision); err != nil {
			rows.Close()
			return err
		}
		successors = append(successors, successor)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, successor := range successors {
		// A manual due-date reset intentionally decouples this period from older snapshots.
		if successor.periodRevision > 0 {
			continue
		}
		basePeriodEnd := successor.basePeriodEnd.Add(inheritedDelta)
		paidDates, err := subscriptionPaidDates(tx, successor.id, successor.periodRevision)
		if err != nil {
			return err
		}
		periodEnd := recomputePeriodEnd(basePeriodEnd, paidDates, successor.durationDays)
		result, err := updateSubscriptionPeriod(tx, successor.id, basePeriodEnd, periodEnd)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return sql.ErrNoRows
		}
		if err := recomputeSuccessorPeriods(tx, successor.id, periodEnd.Sub(successor.currentPeriodEnd)); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionPaidDates(tx *sql.Tx, subscriptionID, periodRevision int) ([]time.Time, error) {
	rows, err := tx.Query(`SELECT "paidAt" FROM payments
		WHERE "subscriptionId" = $1 AND status = 'paid' AND "paidAt" IS NOT NULL
			AND "entitlementRevision" = $2
		ORDER BY "paidAt", id`, subscriptionID, periodRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paidDates := []time.Time{}
	for rows.Next() {
		var paidAt time.Time
		if err := rows.Scan(&paidAt); err != nil {
			return nil, err
		}
		paidDates = append(paidDates, paidAt)
	}
	return paidDates, rows.Err()
}

func updateSubscriptionPeriod(tx *sql.Tx, subscriptionID int, basePeriodEnd, periodEnd time.Time) (sql.Result, error) {
	return tx.Exec(`UPDATE subscriptions SET
		"basePeriodEnd" = $1,
		"currentPeriodEnd" = $2,
		status = CASE
			WHEN status NOT IN ('trialing', 'active', 'past_due') THEN status
			WHEN status = 'trialing' AND $2 >= now() THEN 'trialing'
			WHEN $2 < now() THEN 'past_due'
			ELSE 'active'
		END,
		"updatedAt" = now()
		WHERE id = $3`, basePeriodEnd, periodEnd, subscriptionID)
}

func recomputePeriodEnd(basePeriodEnd time.Time, paidDates []time.Time, durationDays int) time.Time {
	periodEnd := basePeriodEnd
	for _, paidAt := range paidDates {
		periodEnd = advanceSubscriptionPeriodEnd(periodEnd, paidAt, durationDays)
	}
	return periodEnd
}

func MapMercadoPagoPaymentStatus(providerStatus string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(providerStatus)) {
	case "approved":
		return PaymentStatusPaid, nil
	case "pending":
		return PaymentStatusPending, nil
	case "in_process":
		return PaymentStatusProcessing, nil
	case "rejected":
		return PaymentStatusFailed, nil
	case "cancelled", "canceled":
		return PaymentStatusCanceled, nil
	case "refunded", "charged_back":
		return PaymentStatusRefunded, nil
	default:
		return "", ErrUnknownMercadoPagoStatus
	}
}

func MapAbacatePayPaymentStatus(providerStatus string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(providerStatus)) {
	case "PENDING":
		return PaymentStatusPending, nil
	case "PAID":
		return PaymentStatusPaid, nil
	case "EXPIRED", "CANCELLED":
		return PaymentStatusCanceled, nil
	case "REFUNDED":
		return PaymentStatusRefunded, nil
	default:
		return "", ErrUnknownAbacatePayStatus
	}
}

func isValidPaymentStatus(status string) bool {
	switch status {
	case PaymentStatusPending, PaymentStatusProcessing, PaymentStatusPaid, PaymentStatusFailed,
		PaymentStatusCanceled, PaymentStatusRefunded:
		return true
	default:
		return false
	}
}

func nextPaymentStatus(currentStatus, mappedStatus string) string {
	if currentStatus == PaymentStatusRefunded {
		return PaymentStatusRefunded
	}
	if currentStatus == PaymentStatusPaid && mappedStatus != PaymentStatusRefunded {
		return PaymentStatusPaid
	}
	return mappedStatus
}

func advanceSubscriptionPeriodEnd(currentEnd, paidAt time.Time, durationDays int) time.Time {
	base := currentEnd
	if base.Before(paidAt) {
		base = paidAt
	}
	return base.AddDate(0, 0, durationDays)
}

func paymentDueDate(currentPeriodEnd, now time.Time) time.Time {
	if currentPeriodEnd.After(now) {
		return currentPeriodEnd
	}
	return now.AddDate(0, 0, 1)
}

func scanSubscription(scanner rowScanner) (Subscription, error) {
	var subscription Subscription
	var canceledAt sql.NullTime
	err := scanner.Scan(&subscription.Id, &subscription.CompanyId, &subscription.PlanId, &subscription.Status,
		&subscription.PriceCents, &subscription.DurationDays, &subscription.BillingCycle, &subscription.StartsAt, &subscription.CurrentPeriodStart, &subscription.CurrentPeriodEnd,
		&subscription.BasePeriodEnd, &canceledAt, &subscription.CreatedAt, &subscription.UpdatedAt)
	subscription.CanceledAt = nullTimePointer(canceledAt)
	return subscription, err
}

func UpdateCurrentSubscriptionDueDate(companyID int, dueDate time.Time) (Subscription, error) {
	tx, err := db.Begin()
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	current, err := scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions
		WHERE "companyId" = $1 AND status IN ('trialing', 'active', 'past_due')
		FOR UPDATE`, companyID))
	if err != nil {
		return Subscription{}, err
	}
	if !dueDate.After(current.CurrentPeriodStart) {
		return Subscription{}, ErrInvalidSubscriptionDueDate
	}
	if _, err := tx.Exec(`UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
		WHERE "subscriptionId" = $1 AND status = 'pending' AND (
			("providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL
				AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL AND "checkoutCreating" = false)
			OR (provider = 'mercado_pago' AND "dueDate" <= now())
		)`, current.Id); err != nil {
		return Subscription{}, err
	}
	var hasOpenPayments bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments
		WHERE "subscriptionId" = $1 AND status IN ('pending', 'processing'))`, current.Id).Scan(&hasOpenPayments); err != nil {
		return Subscription{}, err
	}
	if hasOpenPayments {
		return Subscription{}, ErrOpenPayments
	}
	updated, err := scanSubscription(tx.QueryRow(`UPDATE subscriptions SET
		"currentPeriodEnd" = $1,
		"basePeriodEnd" = $1,
		"periodRevision" = "periodRevision" + 1,
		status = CASE
			WHEN status = 'trialing' AND $1 >= now() THEN 'trialing'
			WHEN "priceCents" = 0 OR $1 >= now() THEN 'active'
			ELSE 'past_due'
		END,
		"updatedAt" = now()
		WHERE id = $2
		RETURNING id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
			"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"`,
		dueDate, current.Id))
	if err != nil {
		return Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, err
	}
	return updated, nil
}

func scanPayment(scanner rowScanner) (Payment, error) {
	var payment Payment
	var providerPaymentID, providerPreferenceID, checkoutURL, receiptURL, checkoutKey sql.NullString
	var paidAt sql.NullTime
	err := scanner.Scan(&payment.Id, &payment.CompanyId, &payment.SubscriptionId, &payment.Status, &payment.AmountCents,
		&payment.DueDate, &payment.Provider, &providerPaymentID, &providerPreferenceID, &checkoutURL, &receiptURL, &checkoutKey, &paidAt,
		&payment.CreatedAt, &payment.UpdatedAt)
	payment.ProviderPaymentId = nullStringPointer(providerPaymentID)
	payment.ProviderPreferenceId = nullStringPointer(providerPreferenceID)
	payment.CheckoutURL = nullStringPointer(checkoutURL)
	payment.ReceiptURL = nullStringPointer(receiptURL)
	payment.CheckoutKey = nullStringPointer(checkoutKey)
	payment.PaidAt = nullTimePointer(paidAt)
	return payment, err
}
