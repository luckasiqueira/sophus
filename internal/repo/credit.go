package repo

import (
	"database/sql"
	"errors"
	"time"
)

type CreditEntry struct {
	ID                    int64     `json:"id"`
	SourcePaymentID       *int      `json:"sourcePaymentId"`
	PaymentID             *int      `json:"paymentId"`
	ProviderTransactionID string    `json:"providerTransactionId,omitempty"`
	Kind                  string    `json:"kind"`
	AmountCents           int64     `json:"amountCents"`
	CreatedAt             time.Time `json:"createdAt"`
}

type CreditSummary struct {
	BalanceCents int64         `json:"balanceCents"`
	Entries      []CreditEntry `json:"entries"`
}

func GetCompanyCreditSummary(companyID int) (CreditSummary, error) {
	result := CreditSummary{Entries: []CreditEntry{}}
	if err := db.QueryRow(`SELECT COALESCE((SELECT "balanceCents" FROM company_credit_balances WHERE "companyId" = $1), 0)`, companyID).Scan(&result.BalanceCents); err != nil {
		return result, err
	}
	rows, err := db.Query(`SELECT id, "sourcePaymentId", "paymentId", "providerTransactionId", kind, "amountCents", "createdAt"
		FROM company_credit_entries WHERE "companyId" = $1 AND "amountCents" <> 0 ORDER BY id DESC LIMIT 30`, companyID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry CreditEntry
		var source, payment sql.NullInt64
		if err := rows.Scan(&entry.ID, &source, &payment, &entry.ProviderTransactionID, &entry.Kind, &entry.AmountCents, &entry.CreatedAt); err != nil {
			return result, err
		}
		if source.Valid {
			id := int(source.Int64)
			entry.SourcePaymentID = &id
		}
		if payment.Valid {
			id := int(payment.Int64)
			entry.PaymentID = &id
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}

// The company row serializes credit and checkout mutations within one company.
func lockCompanyCredits(tx *sql.Tx, companyID int) (int64, error) {
	var id int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO company_credit_balances ("companyId") VALUES ($1) ON CONFLICT DO NOTHING`, companyID); err != nil {
		return 0, err
	}
	var balance int64
	err := tx.QueryRow(`SELECT "balanceCents" FROM company_credit_balances WHERE "companyId" = $1 FOR UPDATE`, companyID).Scan(&balance)
	return balance, err
}

func addCreditEntry(tx *sql.Tx, companyID int, sourceID, paymentID *int, kind, transactionID string, amount int64) (bool, error) {
	var entryID int64
	err := tx.QueryRow(`INSERT INTO company_credit_entries ("companyId", "sourcePaymentId", "paymentId", "providerTransactionId", kind, "amountCents")
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING RETURNING id`, companyID, sourceID, paymentID, transactionID, kind, amount).Scan(&entryID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`UPDATE company_credit_balances SET "balanceCents" = "balanceCents" + $1, "updatedAt" = now() WHERE "companyId" = $2`, amount, companyID)
	return true, err
}

func releasePaymentCredits(tx *sql.Tx, companyID, paymentID int) error {
	var amount int64
	if err := tx.QueryRow(`SELECT "creditAppliedCents" FROM payments WHERE id = $1`, paymentID).Scan(&amount); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	if _, err := lockCompanyCredits(tx, companyID); err != nil {
		return err
	}
	_, err := addCreditEntry(tx, companyID, nil, &paymentID, "release", "", amount)
	return err
}

func creditAmountToApply(balance, due int64) int64 {
	if balance <= 0 || due <= 0 {
		return 0
	}
	if balance < due {
		return balance
	}
	return due
}

func paymentCanUseCredits(payment Payment, applied int64, creating bool) bool {
	return applied == 0 && payment.Status == PaymentStatusPending && !creating &&
		payment.CheckoutURL == nil && payment.CheckoutKey == nil && payment.ProviderPreferenceId == nil &&
		payment.ProviderPaymentId == nil && payment.AmountCents > 0
}

// ApplyCompanyCreditsToPayment reserves balance exactly once before a checkout
// is issued. If credit covers the entire invoice, entitlement is activated in
// the same transaction and no external checkout is created.
func ApplyCompanyCreditsToPayment(companyID, paymentID int) (Payment, error) {
	defer invalidateAccessCache()
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	balance, err := lockCompanyCredits(tx, companyID)
	if err != nil {
		return Payment{}, err
	}
	payment, err := scanPayment(tx.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, paymentID, companyID))
	if err != nil {
		return Payment{}, err
	}
	var applied int64
	var creating bool
	if err := tx.QueryRow(`SELECT "creditAppliedCents", "checkoutCreating" FROM payments WHERE id = $1`, paymentID).Scan(&applied, &creating); err != nil {
		return Payment{}, err
	}
	if balance <= 0 || !paymentCanUseCredits(payment, applied, creating) {
		return payment, tx.Commit()
	}
	amount := creditAmountToApply(balance, payment.AmountCents)
	if _, err := addCreditEntry(tx, companyID, nil, &paymentID, "apply", "", -amount); err != nil {
		return Payment{}, err
	}
	if _, err := tx.Exec(`UPDATE payments SET "amountCents" = "amountCents" - $1, "creditAppliedCents" = $1, "updatedAt" = now() WHERE id = $2`, amount, paymentID); err != nil {
		return Payment{}, err
	}
	if amount == payment.AmountCents {
		now := time.Now().UTC()
		payment, _, err = applyProviderPayment(tx, paymentID, payment.Provider, "", PaymentStatusPaid, &now, nil)
		if err != nil {
			return Payment{}, err
		}
	} else {
		payment.AmountCents -= amount
	}
	return payment, tx.Commit()
}
