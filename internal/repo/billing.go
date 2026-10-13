package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUnknownMercadoPagoStatus = errors.New("unknown Mercado Pago payment status")
	ErrUnknownAbacatePayStatus  = errors.New("unknown AbacatePay payment status")
	ErrProviderPaymentMismatch  = errors.New("provider payment does not match the internal payment")
	ErrDiscardedPayment         = errors.New("checkout belongs to a discarded payment")
	ErrManuallyConfirmedPayment = errors.New("checkout belongs to a manually confirmed payment")
	ErrInvalidPaymentStatus     = errors.New("invalid internal payment status")
	ErrOpenPayments             = errors.New("subscription has open payments")
	ErrNoSubscriptionChange     = errors.New("subscription plan and billing cycle are unchanged")
	ErrRenewalNotDue            = errors.New("renewal invoice is not due yet")
)

const renewalInvoiceLeadDays = 10

func renewalInvoiceDue(dueDate, now time.Time) bool {
	return !dueDate.After(now.AddDate(0, 0, renewalInvoiceLeadDays))
}

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

func RequestCompanyPlanChange(companyID, planID int, billingCycle, provider string) (Payment, error) {
	defer invalidateAccessCache()
	if !IsValidBillingCycle(billingCycle) {
		return Payment{}, ErrInvalidBillingCycle
	}
	if !IsValidPaymentProvider(provider) {
		return Payment{}, ErrInvalidPaymentProvider
	}
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, err
	}
	plan, err := scanPlan(tx.QueryRow(`SELECT id, name, description, "monthlyPriceCents", "annualPriceCents", "agentLimit",
		"connectionLimit", "trialDays", "storageLimitGB", "isActive", "createdAt", "updatedAt", "allowAddons"
		FROM plans WHERE id = $1 AND "isActive" = true FOR SHARE`, planID))
	if err != nil {
		return Payment{}, err
	}
	priceCents, durationDays, err := BillingCycleTerms(plan, billingCycle)
	if err != nil {
		return Payment{}, err
	}
	current, err := scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions WHERE "companyId" = $1 AND status IN ('trialing', 'active', 'past_due') FOR UPDATE`, companyID))
	if err != nil {
		return Payment{}, err
	}
	if current.PlanId == plan.Id && current.BillingCycle == billingCycle {
		return Payment{}, ErrNoSubscriptionChange
	}
	var addonQuantities AddonQuantities
	var addonPrices AddonPrices
	var quantitiesJSON, pricesJSON []byte
	addonErr := tx.QueryRow(`SELECT quantities,prices FROM subscription_addons WHERE "subscriptionId"=$1`, current.Id).Scan(&quantitiesJSON, &pricesJSON)
	if addonErr != nil && !errors.Is(addonErr, sql.ErrNoRows) {
		return Payment{}, addonErr
	}
	if plan.AllowAddons && addonErr == nil {
		if err := json.Unmarshal(quantitiesJSON, &addonQuantities); err != nil {
			return Payment{}, err
		}
		if err := json.Unmarshal(pricesJSON, &addonPrices); err != nil {
			return Payment{}, err
		}
		monthly, err := addonMonthlyPrice(addonQuantities, addonPrices)
		if err != nil {
			return Payment{}, err
		}
		extra, err := addonCyclePrice(monthly, billingCycle)
		if err != nil || priceCents > 9007199254740991-extra {
			return Payment{}, ErrInvalidAddons
		}
		priceCents += extra
	}
	var agentCount, connectionCount int
	var storageUsedBytes int64
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM agents WHERE "companyId" = $1),
		(SELECT COUNT(*) FROM connections WHERE "companyId" = $1),
		COALESCE((SELECT "usedBytes" + "reservedBytes" FROM company_storage_usage WHERE "companyId" = $1), 0)`, companyID).Scan(&agentCount, &connectionCount, &storageUsedBytes); err != nil {
		return Payment{}, err
	}
	if agentCount > plan.AgentLimit+addonQuantities.ExtraAgents || connectionCount > plan.ConnectionLimit+addonQuantities.ExtraConnections || storageUsedBytes >= int64(plan.StorageLimitGB+addonQuantities.ExtraStorageGB)*(1<<30) {
		return Payment{}, ErrPlanLimitBelowUsage
	}
	var hasOpenPayments bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "companyId" = $1 AND status IN ('pending', 'processing'))`, companyID).Scan(&hasOpenPayments); err != nil {
		return Payment{}, err
	}
	if hasOpenPayments {
		return Payment{}, ErrOpenPayments
	}
	dueDate := paymentDueDate(current.CurrentPeriodEnd, time.Now().UTC())
	payment, err := scanPayment(tx.QueryRow(`INSERT INTO payments
		("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
		VALUES ($1, $2, $3, $4, $5, $6, (SELECT "periodRevision" FROM subscriptions WHERE id = $2))
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		companyID, current.Id, PaymentStatusPending, priceCents, dueDate, provider))
	if err != nil {
		return Payment{}, err
	}
	if _, err := tx.Exec(`INSERT INTO subscription_plan_changes
		("paymentId", "companyId", "sourceSubscriptionId", "targetPlanId", "billingCycle", "priceCents", "durationDays")
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, payment.Id, companyID, current.Id, plan.Id, billingCycle, priceCents, durationDays); err != nil {
		return Payment{}, err
	}
	if priceCents == 0 {
		now := time.Now().UTC()
		payment, err = scanPayment(tx.QueryRow(`UPDATE payments SET status = 'paid', "paidAt" = $1, "updatedAt" = now()
			WHERE id = $2 RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`, now, payment.Id))
		if err != nil {
			return Payment{}, err
		}
		if err := activatePlanChange(tx, payment.Id, now); err != nil {
			return Payment{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, err
	}
	return payment, nil
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

// RestoreAbacatePayCheckoutIdentity repairs older pending checkouts only after
// the caller has verified their identity against the payment provider.
func RestoreAbacatePayCheckoutIdentity(paymentID, companyID int, originalURL, checkoutID, checkoutKey string) (Payment, error) {
	return scanPayment(db.QueryRow(`UPDATE payments SET "providerPaymentId" = $1, "providerPreferenceId" = $1,
		"checkoutKey" = $2, "updatedAt" = now()
		WHERE id = $3 AND "companyId" = $4 AND provider = 'abacate_pay' AND status = 'pending'
		AND "checkoutURL" = $5 AND "checkoutCreating" = false
		AND ("providerPaymentId" IS NULL OR "providerPaymentId" = $1)
		AND ("providerPreferenceId" IS NULL OR "providerPreferenceId" = $1)
		AND ("checkoutKey" IS NULL OR "checkoutKey" = $2)
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		checkoutID, checkoutKey, paymentID, companyID, originalURL))
}

// CancelOrphanAbacatePayCheckout retires an untraceable legacy checkout only
// when it has no provider identity and is not tied to fixed or plan-change entitlements.
func CancelOrphanAbacatePayCheckout(paymentID, companyID int, originalURL string) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE payments SET status = 'canceled', "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2 AND provider = 'abacate_pay' AND status = 'pending'
		AND "checkoutURL" = $3 AND "checkoutCreating" = false
		AND NULLIF(BTRIM("providerPaymentId"), '') IS NULL
		AND NULLIF(BTRIM("providerPreferenceId"), '') IS NULL
		AND NULLIF(BTRIM("checkoutKey"), '') IS NULL
		AND NOT EXISTS (SELECT 1 FROM payment_fixed_entitlements WHERE "paymentId" = payments.id)
		AND NOT EXISTS (SELECT 1 FROM subscription_plan_changes WHERE "paymentId" = payments.id)`, paymentID, companyID, originalURL)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 1 {
		if err := releasePaymentCredits(tx, companyID, paymentID); err != nil {
			return false, err
		}
	}
	return rows == 1, tx.Commit()
}

func GetPaymentById(id int) (Payment, error) {
	return scanPayment(db.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1`, id))
}

func GetPaymentPlanID(paymentID int) (int, error) {
	var planID int
	err := db.QueryRow(`SELECT COALESCE(change."targetPlanId", subscription."planId")
		FROM payments payment
		JOIN subscriptions subscription ON subscription.id = payment."subscriptionId"
		LEFT JOIN subscription_plan_changes change ON change."paymentId" = payment.id
		WHERE payment.id = $1`, paymentID).Scan(&planID)
	return planID, err
}

func IsPlanChangePayment(paymentID int) (bool, error) {
	var planChange bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM subscription_plan_changes WHERE "paymentId" = $1)`, paymentID).Scan(&planChange)
	return planChange, err
}

func GetPayments() ([]Payment, error) {
	rows, err := db.Query(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments ORDER BY "createdAt" DESC, id DESC`)
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

func UpdatePendingPaymentDueDate(id, companyID int, dueDate time.Time) (Payment, error) {
	return scanPayment(db.QueryRow(`UPDATE payments SET "dueDate" = $1, "updatedAt" = now()
		WHERE id = $2 AND "companyId" = $3 AND status = 'pending' AND "checkoutCreating" = false
			AND "providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL
			AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL
			AND NOT EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id)
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		dueDate, id, companyID))
}

// ClearUnlinkedPaymentCheckout clears a retry key only after the caller has
// confirmed that no payable checkout remains at the provider.
func ClearUnlinkedPaymentCheckout(id, companyID int, checkoutKey string) error {
	result, err := db.Exec(`UPDATE payments SET "checkoutKey" = NULL, "checkoutCreating" = false,
		"checkoutLease" = NULL, "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2 AND status = 'pending'
		AND ("checkoutCreating" = false OR "updatedAt" < now() - interval '5 minutes')
		AND "providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL
		AND "checkoutKey" = $3`, id, companyID, checkoutKey)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func ReleaseStaleUnlinkedPaymentCheckout(id, companyID int) error {
	_, err := db.Exec(`UPDATE payments SET "checkoutCreating" = false, "checkoutLease" = NULL, "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2 AND status = 'pending' AND "checkoutCreating" = true
		AND "updatedAt" < now() - interval '5 minutes'
		AND "checkoutKey" IS NULL AND "providerPaymentId" IS NULL
		AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL`, id, companyID)
	return err
}

// UpdatePendingPaymentDetails applies a one-off invoice amount. Issued Mercado
// Pago preferences must be updated by the caller before this is invoked.
func UpdatePendingPaymentDetails(id, companyID int, amountCents int64, dueDate time.Time, expectedPreferenceID string) (Payment, error) {
	return scanPayment(db.QueryRow(`UPDATE payments SET "amountCents" = $1, "dueDate" = $2,
		"manualAmount" = true, "updatedAt" = now()
		WHERE id = $3 AND "companyId" = $4 AND status = 'pending' AND "checkoutCreating" = false
			AND "providerPaymentId" IS NULL AND "creditAppliedCents" = 0
			AND (("providerPreferenceId" IS NULL AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL AND $5 = '')
				OR (provider = 'mercado_pago' AND "providerPreferenceId" = $5 AND "checkoutURL" IS NOT NULL AND "checkoutKey" IS NOT NULL AND $5 <> ''))
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		amountCents, dueDate, id, companyID, expectedPreferenceID))
}

func CancelPendingPayment(id, companyID int) (Payment, error) {
	return cancelPendingPayment(id, companyID, "")
}

func CancelPendingMercadoPagoPayment(id, companyID int, preferenceID string) (Payment, error) {
	if preferenceID == "" {
		return Payment{}, sql.ErrNoRows
	}
	return cancelPendingPayment(id, companyID, preferenceID)
}

func cancelPendingPayment(id, companyID int, expectedPreferenceID string) (Payment, error) {
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, err
	}
	payment, err := scanPayment(tx.QueryRow(`UPDATE payments SET status = 'canceled', "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2 AND status = 'pending' AND "checkoutCreating" = false
			AND "providerPaymentId" IS NULL
			AND (("providerPreferenceId" IS NULL AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL AND $3 = '')
				OR (provider = 'mercado_pago' AND "providerPreferenceId" = $3 AND "checkoutURL" IS NOT NULL AND "checkoutKey" IS NOT NULL AND $3 <> ''))
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		id, companyID, expectedPreferenceID))
	if err != nil {
		return Payment{}, err
	}
	// A canceled trial invoice no longer reserves a later paid period. Removing
	// its entitlement also prevents recreating the canceled charge on checkout.
	if _, err := tx.Exec(`DELETE FROM payment_fixed_entitlements WHERE "paymentId" = $1`, id); err != nil {
		return Payment{}, err
	}
	if err := releasePaymentCredits(tx, companyID, id); err != nil {
		return Payment{}, err
	}
	if _, err := tx.Exec(`UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
		WHERE "paymentId" = $1 AND status = 'pending'`, id); err != nil {
		return Payment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, err
	}
	return payment, nil
}

// DiscardPendingAbacatePayPayment retires the local invoice while retaining the
// provider ID and checkout key so delayed webhooks can be recognized and ignored.
func DiscardPendingAbacatePayPayment(id, companyID int, preferenceID, checkoutKey string) (Payment, error) {
	if preferenceID == "" || checkoutKey == "" {
		return Payment{}, sql.ErrNoRows
	}
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, err
	}
	payment, err := scanPayment(tx.QueryRow(`UPDATE payments SET status = 'canceled', "ignoredAt" = now(),
		"checkoutURL" = NULL, "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2 AND provider = 'abacate_pay' AND status = 'pending'
		AND "checkoutCreating" = false AND ("providerPaymentId" IS NULL OR "providerPaymentId" = $3)
		AND "providerPreferenceId" = $3 AND "checkoutKey" = $4 AND "checkoutURL" IS NOT NULL
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		id, companyID, preferenceID, checkoutKey))
	if err != nil {
		return Payment{}, err
	}
	if _, err := tx.Exec(`DELETE FROM payment_fixed_entitlements WHERE "paymentId" = $1`, id); err != nil {
		return Payment{}, err
	}
	if err := releasePaymentCredits(tx, companyID, id); err != nil {
		return Payment{}, err
	}
	if _, err := tx.Exec(`UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
		WHERE "paymentId" = $1 AND status = 'pending'`, id); err != nil {
		return Payment{}, err
	}
	return payment, tx.Commit()
}

// ConfirmPendingPaymentManually activates the same entitlement as a provider
// payment, atomically with marking the invoice as manually confirmed.
func ConfirmPendingPaymentManually(paymentID, companyID, adminID int, reason string) (Payment, error) {
	defer invalidateAccessCache()
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	var lockedCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return Payment{}, err
	}
	now := time.Now().UTC()
	var provider string
	err = tx.QueryRow(`UPDATE payments SET "manualConfirmedAt" = $1, "manualConfirmedBy" = $2,
		"manualPaymentReason" = $3, "checkoutURL" = NULL, "updatedAt" = now()
		WHERE id = $4 AND "companyId" = $5 AND status = 'pending'
		AND ("providerPaymentId" IS NULL OR (provider = 'abacate_pay' AND "providerPaymentId" = "providerPreferenceId"))
		AND "checkoutCreating" = false AND "amountCents" > 0 AND "ignoredAt" IS NULL
		RETURNING provider`, now, adminID, reason, paymentID, companyID).Scan(&provider)
	if err != nil {
		return Payment{}, err
	}
	payment, _, err := applyProviderPayment(tx, paymentID, provider, "", PaymentStatusPaid, &now, nil)
	if err != nil {
		return Payment{}, err
	}
	return payment, tx.Commit()
}

// RecordDiscardedCheckoutEvent acknowledges a provider event once and makes
// unexpected payments visible to platform administrators without billing users.
func RecordSurplusCheckoutEvent(companyID, paymentID int, provider, eventID, transactionID, status string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockCompanyCredits(tx, companyID); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO provider_webhook_events (provider, "eventId")
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, provider, eventID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		var amount int64
		var ignored bool
		if err := tx.QueryRow(`SELECT "amountCents", ("ignoredAt" IS NOT NULL OR "manualConfirmedAt" IS NOT NULL) FROM payments
			WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, paymentID, companyID).Scan(&amount, &ignored); err != nil {
			return err
		}
		if !ignored {
			return ErrProviderPaymentMismatch
		}
		switch status {
		case PaymentStatusPaid:
			var reversed bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM company_credit_entries WHERE "sourcePaymentId" = $1 AND "providerTransactionId" = $2 AND kind = 'reversal')`, paymentID, transactionID).Scan(&reversed); err != nil {
				return err
			}
			if !reversed {
				if _, err := addCreditEntry(tx, companyID, &paymentID, nil, "credit", transactionID, amount); err != nil {
					return err
				}
			}
		case PaymentStatusRefunded:
			var issued bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM company_credit_entries WHERE "sourcePaymentId" = $1 AND "providerTransactionId" = $2 AND kind = 'credit')`, paymentID, transactionID).Scan(&issued); err != nil {
				return err
			}
			reversal := int64(0)
			if issued {
				reversal = -amount
			}
			if _, err := addCreditEntry(tx, companyID, &paymentID, nil, "reversal", transactionID, reversal); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO audit_events ("companyId", "actorEmail", "actorType", action, "targetType", "targetId")
			VALUES ($1, '', 'provider', $2, 'payment', $3)`, companyID, "surplus_payment_"+status, strconv.Itoa(paymentID)); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	if _, err := tx.Exec(`WITH canceled AS (
		UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
		WHERE "companyId" = $1 AND provider = 'mercado_pago' AND status = 'pending' AND "dueDate" <= now() AND "creditAppliedCents" = 0
			AND NOT EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id)
		RETURNING id
	) UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
		WHERE "paymentId" IN (SELECT id FROM canceled) AND status = 'pending'`, companyID); err != nil {
		return Payment{}, false, err
	}
	payment, err := scanPayment(tx.QueryRow(`SELECT payment.id, payment."companyId", payment."subscriptionId", payment.status,
		payment."amountCents", payment."dueDate", payment.provider, payment."providerPaymentId", payment."providerPreferenceId",
		payment."checkoutURL", payment."receiptURL", payment."checkoutKey", payment."paidAt", payment."createdAt", payment."updatedAt"
		FROM payments payment
		WHERE payment."companyId" = $1 AND payment.status IN ('pending', 'processing')
		AND (EXISTS(SELECT 1 FROM subscription_plan_changes change WHERE change."paymentId"=payment.id AND change.status='pending')
		OR EXISTS(SELECT 1 FROM subscription_addon_changes change WHERE change."paymentId"=payment.id AND change.status='pending'))
		ORDER BY payment."createdAt" DESC LIMIT 1 FOR UPDATE`, companyID))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Payment{}, false, err
		}
		return payment, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
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
	payment, err = scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments
		WHERE "companyId" = $1 AND "subscriptionId" = $2 AND status IN ('pending', 'processing')
			AND ("amountCents" + "creditAppliedCents" = $3 OR ("manualAmount" = true AND "entitlementRevision" = (SELECT "periodRevision" FROM subscriptions WHERE id = $2))
				OR EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id))
			AND ("dueDate" > now() OR (provider = 'abacate_pay' AND status = 'pending')
				OR EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id))
		ORDER BY "createdAt" DESC LIMIT 1 FOR UPDATE`, companyID, subscription.Id,
		subscription.PriceCents))
	if err == nil {
		if payment.Provider != provider && payment.ProviderPaymentId == nil && payment.ProviderPreferenceId == nil &&
			payment.CheckoutURL == nil && payment.CheckoutKey == nil {
			var fixedEntitlement bool
			if updateErr := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payment_fixed_entitlements WHERE "paymentId" = $1)`, payment.Id).Scan(&fixedEntitlement); updateErr != nil {
				return Payment{}, false, updateErr
			}
			if fixedEntitlement {
				payment, err = scanPayment(tx.QueryRow(`UPDATE payments SET provider = $1, "updatedAt" = now() WHERE id = $2
					RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
					"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`, provider, payment.Id))
				if err != nil {
					return Payment{}, false, err
				}
			} else {
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
	if !renewalInvoiceDue(subscription.CurrentPeriodEnd, time.Now().UTC()) {
		return Payment{}, false, ErrRenewalNotDue
	}
	if subscription.Status == SubscriptionStatusTrialing {
		var hasPayment bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "subscriptionId" = $1 AND NOT EXISTS(SELECT 1 FROM subscription_addon_changes addon WHERE addon."paymentId"=payments.id))`, subscription.Id).Scan(&hasPayment); err != nil {
			return Payment{}, false, err
		}
		if !hasPayment {
			trialDays := int(subscription.CurrentPeriodEnd.Sub(subscription.StartsAt).Hours() / 24)
			amountCents, err := proratedTrialAmount(subscription.PriceCents, subscription.DurationDays, trialDays)
			if err != nil {
				return Payment{}, false, err
			}
			payment, err := scanPayment(tx.QueryRow(`INSERT INTO payments
				("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
				VALUES ($1, $2, 'pending', $3, $4, $5, (SELECT "periodRevision" FROM subscriptions WHERE id = $2))
				RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
					"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
				companyID, subscription.Id, amountCents, subscription.CurrentPeriodEnd, provider))
			if err != nil {
				return Payment{}, false, err
			}
			if _, err := tx.Exec(`INSERT INTO payment_fixed_entitlements ("paymentId", "periodEnd") VALUES ($1, $2)`,
				payment.Id, subscription.StartsAt.AddDate(0, 0, subscription.DurationDays)); err != nil {
				return Payment{}, false, err
			}
			if err := tx.Commit(); err != nil {
				return Payment{}, false, err
			}
			return payment, true, nil
		}
	}
	var previousAmount int64
	var previousDueDate, fixedPeriodEnd time.Time
	err = tx.QueryRow(`SELECT payment."amountCents", payment."dueDate", fixed."periodEnd"
		FROM payments payment JOIN payment_fixed_entitlements fixed ON fixed."paymentId" = payment.id
		WHERE payment."subscriptionId" = $1 AND payment.status IN ('failed', 'canceled', 'refunded')
			AND NOT EXISTS(SELECT 1 FROM payments paid_payment
				JOIN payment_fixed_entitlements paid_fixed ON paid_fixed."paymentId" = paid_payment.id
				WHERE paid_payment."subscriptionId" = payment."subscriptionId" AND paid_payment.status = 'paid')
		ORDER BY payment."createdAt" DESC LIMIT 1 FOR UPDATE`, subscription.Id).Scan(&previousAmount, &previousDueDate, &fixedPeriodEnd)
	if err == nil {
		if !renewalInvoiceDue(previousDueDate, time.Now().UTC()) {
			return Payment{}, false, ErrRenewalNotDue
		}
		payment, err = scanPayment(tx.QueryRow(`INSERT INTO payments
			("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
			VALUES ($1, $2, 'pending', $3, $4, $5, (SELECT "periodRevision" FROM subscriptions WHERE id = $2))
			RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
				"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
			companyID, subscription.Id, previousAmount, previousDueDate, provider))
		if err != nil {
			return Payment{}, false, err
		}
		if _, err := tx.Exec(`INSERT INTO payment_fixed_entitlements ("paymentId", "periodEnd") VALUES ($1, $2)`, payment.Id, fixedPeriodEnd); err != nil {
			return Payment{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return Payment{}, false, err
		}
		return payment, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Payment{}, false, err
	}

	dueDate := subscription.CurrentPeriodEnd
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
	defer invalidateAccessCache()
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
	defer invalidateAccessCache()
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
	var subscriptionPriceCents int64
	if err := tx.QueryRow(`SELECT "basePeriodEnd", "currentPeriodEnd", "durationDays", "periodRevision", "priceCents"
		FROM subscriptions WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, subscriptionID, companyID).Scan(
		&basePeriodEnd, &currentPeriodEnd, &durationDays, &periodRevision, &subscriptionPriceCents); err != nil {
		return Payment{}, false, err
	}

	payment, err := scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1 FOR UPDATE`, paymentID))
	if err != nil {
		return Payment{}, false, err
	}
	var ignored, manuallyConfirmed bool
	if err := tx.QueryRow(`SELECT "ignoredAt" IS NOT NULL, "manualConfirmedAt" IS NOT NULL FROM payments WHERE id = $1`, paymentID).Scan(&ignored, &manuallyConfirmed); err != nil {
		return Payment{}, false, err
	}
	if ignored {
		return Payment{}, false, ErrDiscardedPayment
	}
	if manuallyConfirmed && providerPaymentID != "" {
		return Payment{}, false, ErrManuallyConfirmedPayment
	}
	if payment.ProviderPaymentId != nil && *payment.ProviderPaymentId != providerPaymentID && !(manuallyConfirmed && providerPaymentID == "") {
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
	if nextStatus == PaymentStatusCanceled || nextStatus == PaymentStatusFailed || nextStatus == PaymentStatusRefunded {
		if err := releasePaymentCredits(tx, companyID, paymentID); err != nil {
			return Payment{}, false, err
		}
	}
	if nextStatus == PaymentStatusCanceled || nextStatus == PaymentStatusFailed {
		if _, err := tx.Exec(`UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
			WHERE "paymentId" = $1 AND status = 'pending'`, paymentID); err != nil {
			return Payment{}, false, err
		}
	}

	if firstPaidTransition || firstRefundTransition {
		addon, addonErr := applyAddonPayment(tx, payment, firstPaidTransition, firstRefundTransition)
		if addonErr != nil {
			return Payment{}, false, addonErr
		}
		if addon {
			return payment, firstPaidTransition, nil
		}
		var planChangeStatus sql.NullString
		if err := tx.QueryRow(`SELECT status FROM subscription_plan_changes WHERE "paymentId" = $1 FOR UPDATE`, paymentID).Scan(&planChangeStatus); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Payment{}, false, err
		}
		if planChangeStatus.Valid {
			switch {
			case firstPaidTransition && planChangeStatus.String == "pending":
				if err := activatePlanChange(tx, paymentID, *effectivePaidAt); err != nil {
					return Payment{}, false, err
				}
			case firstPaidTransition && planChangeStatus.String != "applied":
				return Payment{}, false, ErrProviderPaymentMismatch
			case firstRefundTransition && planChangeStatus.String == "applied":
				if err := expireRefundedPlanChange(tx, paymentID); err != nil {
					return Payment{}, false, err
				}
			}
		} else {
			if err := recomputeSubscriptionPeriod(tx, subscriptionID, basePeriodEnd, currentPeriodEnd, durationDays,
				periodRevision); err != nil {
				return Payment{}, false, err
			}
			if firstPaidTransition {
				if _, err := tx.Exec(`UPDATE subscriptions SET status = 'active', "updatedAt" = now()
					WHERE id = $1 AND status IN ('trialing', 'expired') AND "currentPeriodEnd" >= now()
						AND NOT EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed JOIN payments fixed_payment ON fixed_payment.id = fixed."paymentId"
							WHERE fixed."paymentId" = $2 AND fixed_payment."dueDate" > now())`, subscriptionID, paymentID); err != nil {
					return Payment{}, false, err
				}
			}
		}
	}
	if firstPaidTransition {
		if err := createNextRenewalPayment(tx, payment, subscriptionPriceCents, periodRevision); err != nil {
			return Payment{}, false, err
		}
	}

	return payment, firstPaidTransition, nil
}

func createNextRenewalPayment(tx *sql.Tx, payment Payment, amountCents int64, periodRevision int) error {
	var planChange bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM subscription_plan_changes WHERE "paymentId" = $1)`, payment.Id).Scan(&planChange); err != nil {
		return err
	}
	if planChange || amountCents <= 0 {
		return nil
	}
	var openPayment bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "subscriptionId" = $1 AND status IN ('pending', 'processing'))`, payment.SubscriptionId).Scan(&openPayment); err != nil {
		return err
	}
	if openPayment {
		return nil
	}
	var dueDate time.Time
	var fixedEntitlement bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payment_fixed_entitlements WHERE "paymentId" = $1)`, payment.Id).Scan(&fixedEntitlement); err != nil {
		return err
	}
	if fixedEntitlement {
		if err := tx.QueryRow(`SELECT "periodEnd" FROM payment_fixed_entitlements
			WHERE "paymentId" = $1 AND "renewalPaymentId" IS NULL FOR UPDATE`, payment.Id).Scan(&dueDate); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
	} else if err := tx.QueryRow(`SELECT "currentPeriodEnd" FROM subscriptions WHERE id = $1`, payment.SubscriptionId).Scan(&dueDate); err != nil {
		return err
	}
	if !renewalInvoiceDue(dueDate, time.Now().UTC()) {
		return nil
	}
	var renewalPaymentID int
	if err := tx.QueryRow(`INSERT INTO payments
		("companyId", "subscriptionId", status, "amountCents", "dueDate", provider, "entitlementRevision")
		VALUES ($1, $2, 'pending', $3, $4, $5, $6) RETURNING id`, payment.CompanyId, payment.SubscriptionId,
		amountCents, dueDate, payment.Provider, periodRevision).Scan(&renewalPaymentID); err != nil {
		return err
	}
	if fixedEntitlement {
		_, err := tx.Exec(`UPDATE payment_fixed_entitlements SET "renewalPaymentId" = $1 WHERE "paymentId" = $2`, renewalPaymentID, payment.Id)
		return err
	}
	return nil
}

func activatePlanChange(tx *sql.Tx, paymentID int, paidAt time.Time) error {
	var companyID, sourceSubscriptionID, targetPlanID, durationDays int
	var billingCycle string
	var priceCents int64
	if err := tx.QueryRow(`SELECT "companyId", "sourceSubscriptionId", "targetPlanId", "billingCycle", "priceCents", "durationDays"
		FROM subscription_plan_changes WHERE "paymentId" = $1 AND status = 'pending' FOR UPDATE`, paymentID).Scan(
		&companyID, &sourceSubscriptionID, &targetPlanID, &billingCycle, &priceCents, &durationDays); err != nil {
		return err
	}
	source, err := scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, sourceSubscriptionID, companyID))
	if err != nil {
		return err
	}
	periodEnd := advanceSubscriptionPeriodEnd(source.CurrentPeriodEnd, paidAt, durationDays)
	if _, err := tx.Exec(`UPDATE subscriptions SET status = 'canceled', "canceledAt" = now(), "updatedAt" = now()
		WHERE id = $1 AND status IN ('trialing', 'active', 'past_due')`, sourceSubscriptionID); err != nil {
		return err
	}
	var targetSubscriptionID int
	if err := tx.QueryRow(`INSERT INTO subscriptions
		("companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		 "currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "previousSubscriptionId")
		VALUES ($1, $2, 'active', $3, $4, $5, $6, $6, $7, $7, $8) RETURNING id`,
		companyID, targetPlanID, priceCents, durationDays, billingCycle, paidAt, periodEnd, sourceSubscriptionID).Scan(&targetSubscriptionID); err != nil {
		return err
	}
	if err := carrySubscriptionAddons(tx, sourceSubscriptionID, targetSubscriptionID, companyID, targetPlanID, billingCycle, priceCents); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE subscription_plan_changes SET status = 'applied', "targetSubscriptionId" = $1, "updatedAt" = now()
		WHERE "paymentId" = $2 AND status = 'pending'`, targetSubscriptionID, paymentID)
	return err
}

func expireRefundedPlanChange(tx *sql.Tx, paymentID int) error {
	var targetSubscriptionID sql.NullInt64
	if err := tx.QueryRow(`SELECT "targetSubscriptionId" FROM subscription_plan_changes WHERE "paymentId" = $1 FOR UPDATE`, paymentID).Scan(&targetSubscriptionID); err != nil {
		return err
	}
	if !targetSubscriptionID.Valid {
		return nil
	}
	var otherPaidPayments bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "subscriptionId" = $1 AND status = 'paid' AND NOT EXISTS(SELECT 1 FROM subscription_addon_changes addon WHERE addon."paymentId"=payments.id))`, targetSubscriptionID.Int64).Scan(&otherPaidPayments); err != nil {
		return err
	}
	if otherPaidPayments {
		return nil
	}
	_, err := tx.Exec(`UPDATE subscriptions SET status = 'expired', "updatedAt" = now()
		WHERE id = $1 AND status IN ('trialing', 'active', 'past_due')`, targetSubscriptionID.Int64)
	return err
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
	paidEntitlements, err := subscriptionPaidEntitlements(tx, subscriptionID, periodRevision)
	if err != nil {
		return err
	}
	periodEnd := recomputeEntitlementPeriodEnd(basePeriodEnd, paidEntitlements, durationDays)
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
		paidEntitlements, err := subscriptionPaidEntitlements(tx, successor.id, successor.periodRevision)
		if err != nil {
			return err
		}
		periodEnd := recomputeEntitlementPeriodEnd(basePeriodEnd, paidEntitlements, successor.durationDays)
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

type paidEntitlement struct {
	paidAt         time.Time
	fixedPeriodEnd *time.Time
}

func subscriptionPaidEntitlements(tx *sql.Tx, subscriptionID, periodRevision int) ([]paidEntitlement, error) {
	rows, err := tx.Query(`SELECT payment."paidAt", fixed."periodEnd" FROM payments payment
		LEFT JOIN payment_fixed_entitlements fixed ON fixed."paymentId" = payment.id
		WHERE payment."subscriptionId" = $1 AND payment.status = 'paid' AND payment."paidAt" IS NOT NULL
			AND NOT EXISTS(SELECT 1 FROM subscription_addon_changes addons WHERE addons."paymentId"=payment.id)
			AND payment."entitlementRevision" = $2
		ORDER BY payment."paidAt", payment.id`, subscriptionID, periodRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entitlements := []paidEntitlement{}
	for rows.Next() {
		var entitlement paidEntitlement
		var fixedPeriodEnd sql.NullTime
		if err := rows.Scan(&entitlement.paidAt, &fixedPeriodEnd); err != nil {
			return nil, err
		}
		entitlement.fixedPeriodEnd = nullTimePointer(fixedPeriodEnd)
		entitlements = append(entitlements, entitlement)
	}
	return entitlements, rows.Err()
}

func updateSubscriptionPeriod(tx *sql.Tx, subscriptionID int, basePeriodEnd, periodEnd time.Time) (sql.Result, error) {
	return tx.Exec(`UPDATE subscriptions SET
		"basePeriodEnd" = $1,
		"currentPeriodEnd" = $2,
		"redemptionEndsAt" = NULL,
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
	entitlements := make([]paidEntitlement, 0, len(paidDates))
	for _, paidAt := range paidDates {
		entitlements = append(entitlements, paidEntitlement{paidAt: paidAt})
	}
	return recomputeEntitlementPeriodEnd(basePeriodEnd, entitlements, durationDays)
}

func recomputeEntitlementPeriodEnd(basePeriodEnd time.Time, entitlements []paidEntitlement, durationDays int) time.Time {
	periodEnd := basePeriodEnd
	for _, entitlement := range entitlements {
		if entitlement.fixedPeriodEnd != nil {
			if entitlement.fixedPeriodEnd.After(periodEnd) {
				periodEnd = *entitlement.fixedPeriodEnd
			}
			continue
		}
		periodEnd = advanceSubscriptionPeriodEnd(periodEnd, entitlement.paidAt, durationDays)
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
	defer invalidateAccessCache()
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
	if _, err := tx.Exec(`WITH canceled AS (UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
		WHERE "subscriptionId" = $1 AND status = 'pending' AND (
			("providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL
				AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL AND "checkoutCreating" = false)
			OR (provider = 'mercado_pago' AND "dueDate" <= now() AND "creditAppliedCents" = 0)
		) AND NOT EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id)
		RETURNING id) UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
		WHERE "paymentId" IN (SELECT id FROM canceled) AND status = 'pending'`, current.Id); err != nil {
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
		"redemptionEndsAt" = NULL,
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

func ReactivateCompanySubscription(companyID, platformAdminID, activeDays int, reason string) (Subscription, error) {
	defer invalidateAccessCache()
	reason = strings.TrimSpace(reason)
	if !validSubscriptionAdjustment(activeDays, 3650, reason) {
		return Subscription{}, ErrInvalidSubscriptionAdjustment
	}
	tx, err := db.Begin()
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	current, err := latestSubscriptionForUpdate(tx, companyID)
	if err != nil {
		return Subscription{}, err
	}
	if current.Status != SubscriptionStatusPastDue && current.Status != SubscriptionStatusExpired && current.Status != SubscriptionStatusCanceled {
		return Subscription{}, ErrInvalidSubscriptionAdjustment
	}
	if err := cancelStaleAndCheckOpenPayments(tx, current.Id); err != nil {
		return Subscription{}, err
	}
	now := time.Now().UTC()
	periodEnd := now.AddDate(0, 0, activeDays)
	updated, err := scanSubscription(tx.QueryRow(`UPDATE subscriptions SET status = 'active', "currentPeriodStart" = $1,
		"currentPeriodEnd" = $2, "basePeriodEnd" = $2, "redemptionEndsAt" = NULL, "canceledAt" = NULL,
		"periodRevision" = "periodRevision" + 1, "updatedAt" = now() WHERE id = $3
		RETURNING id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
			"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"`, now, periodEnd, current.Id))
	if err != nil {
		return Subscription{}, err
	}
	if err := insertSubscriptionAdjustment(tx, companyID, current, platformAdminID, "reactivate", activeDays, reason, updated.Status, updated.CurrentPeriodEnd, nil); err != nil {
		return Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, err
	}
	return updated, nil
}

func GrantCompanySubscriptionRedemption(companyID, platformAdminID, days int, reason string) (Subscription, time.Time, error) {
	defer invalidateAccessCache()
	reason = strings.TrimSpace(reason)
	if !validSubscriptionAdjustment(days, 365, reason) {
		return Subscription{}, time.Time{}, ErrInvalidSubscriptionAdjustment
	}
	tx, err := db.Begin()
	if err != nil {
		return Subscription{}, time.Time{}, err
	}
	defer tx.Rollback()
	current, err := latestSubscriptionForUpdate(tx, companyID)
	if err != nil {
		return Subscription{}, time.Time{}, err
	}
	if current.Status != SubscriptionStatusPastDue && current.Status != SubscriptionStatusExpired {
		return Subscription{}, time.Time{}, ErrInvalidSubscriptionAdjustment
	}
	var redemptionEndsAt time.Time
	updated, err := scanSubscription(tx.QueryRow(`UPDATE subscriptions SET status = 'past_due', "canceledAt" = NULL,
		"redemptionEndsAt" = GREATEST(COALESCE("redemptionEndsAt", now()), now()) + ($1 * interval '1 day'), "updatedAt" = now()
		WHERE id = $2
		RETURNING id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
			"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"`, days, current.Id))
	if err != nil {
		return Subscription{}, time.Time{}, err
	}
	if err := tx.QueryRow(`SELECT "redemptionEndsAt" FROM subscriptions WHERE id = $1`, current.Id).Scan(&redemptionEndsAt); err != nil {
		return Subscription{}, time.Time{}, err
	}
	if err := insertSubscriptionAdjustment(tx, companyID, current, platformAdminID, "redemption", days, reason, updated.Status, updated.CurrentPeriodEnd, &redemptionEndsAt); err != nil {
		return Subscription{}, time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, time.Time{}, err
	}
	return updated, redemptionEndsAt, nil
}

func validSubscriptionAdjustment(days, maximum int, reason string) bool {
	reason = strings.TrimSpace(reason)
	return days >= 1 && days <= maximum && len(reason) >= 3 && len(reason) <= 500
}

func latestSubscriptionForUpdate(tx *sql.Tx, companyID int) (Subscription, error) {
	return scanSubscription(tx.QueryRow(`SELECT id, "companyId", "planId", status, "priceCents", "durationDays", "billingCycle", "startsAt",
		"currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt", "createdAt", "updatedAt"
		FROM subscriptions WHERE "companyId" = $1 ORDER BY "createdAt" DESC, id DESC LIMIT 1 FOR UPDATE`, companyID))
}

func cancelStaleAndCheckOpenPayments(tx *sql.Tx, subscriptionID int) error {
	if _, err := tx.Exec(`WITH canceled AS (UPDATE payments SET status = 'canceled', "checkoutCreating" = false, "updatedAt" = now()
		WHERE "subscriptionId" = $1 AND status = 'pending' AND (
			("providerPaymentId" IS NULL AND "providerPreferenceId" IS NULL AND "checkoutURL" IS NULL AND "checkoutKey" IS NULL AND "checkoutCreating" = false)
			OR (provider = 'mercado_pago' AND "dueDate" <= now() AND "creditAppliedCents" = 0))
			AND NOT EXISTS(SELECT 1 FROM payment_fixed_entitlements fixed WHERE fixed."paymentId" = payments.id)
		RETURNING id)
		UPDATE subscription_plan_changes SET status = 'canceled', "updatedAt" = now()
		WHERE "paymentId" IN (SELECT id FROM canceled) AND status = 'pending'`, subscriptionID); err != nil {
		return err
	}
	var open bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "subscriptionId" = $1 AND status IN ('pending', 'processing'))`, subscriptionID).Scan(&open); err != nil {
		return err
	}
	if open {
		return ErrOpenPayments
	}
	return nil
}

func insertSubscriptionAdjustment(tx *sql.Tx, companyID int, previous Subscription, platformAdminID int, action string, days int, reason, newStatus string, newPeriodEnd time.Time, redemptionEndsAt *time.Time) error {
	_, err := tx.Exec(`INSERT INTO subscription_adjustments ("companyId", "subscriptionId", "platformAdminId", action, days, reason,
		"previousStatus", "newStatus", "previousPeriodEnd", "newPeriodEnd", "redemptionEndsAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, companyID, previous.Id, platformAdminID, action, days,
		reason, previous.Status, newStatus, previous.CurrentPeriodEnd, newPeriodEnd, redemptionEndsAt)
	return err
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
