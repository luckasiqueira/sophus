package repo

import (
	"database/sql"
	"errors"
	"time"
)

// AsaasPaymentLink is one Asaas link issued for an invoice and payment method.
type AsaasPaymentLink struct {
	LinkID      string
	PaymentID   int
	BillingType string
	CheckoutKey string
	CheckoutURL string
	CreatedAt   time.Time
}

// RecordAsaasPaymentLink maps a link to its invoice before the link is shown
// to anyone, so webhooks for charges made through it can always be resolved.
func RecordAsaasPaymentLink(link AsaasPaymentLink) error {
	_, err := db.Exec(`INSERT INTO asaas_payment_links ("linkId", "paymentId", "billingType", "checkoutKey", "checkoutURL")
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT ("linkId") DO NOTHING`,
		link.LinkID, link.PaymentID, link.BillingType, link.CheckoutKey, link.CheckoutURL)
	return err
}

// ListAsaasPaymentLinks returns every link issued for an invoice, newest first.
func ListAsaasPaymentLinks(paymentID int) ([]AsaasPaymentLink, error) {
	rows, err := db.Query(`SELECT "linkId", "paymentId", "billingType", "checkoutKey", "checkoutURL", "createdAt"
		FROM asaas_payment_links WHERE "paymentId" = $1 ORDER BY "createdAt" DESC, "linkId"`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []AsaasPaymentLink
	for rows.Next() {
		var link AsaasPaymentLink
		if err := rows.Scan(&link.LinkID, &link.PaymentID, &link.BillingType, &link.CheckoutKey, &link.CheckoutURL, &link.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// GetPaymentByAsaasLink resolves the invoice that issued a link, including
// links the invoice no longer points to after the payer switched methods.
func GetPaymentByAsaasLink(linkID string) (Payment, error) {
	payment, err := scanPayment(db.QueryRow(`SELECT id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
		"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"
		FROM payments WHERE provider = 'asaas' AND "providerPreferenceId" = $1`, linkID))
	if !errors.Is(err, sql.ErrNoRows) {
		return payment, err
	}
	return scanPayment(db.QueryRow(`SELECT payment.id, payment."companyId", payment."subscriptionId", payment.status, payment."amountCents",
		payment."dueDate", payment.provider, payment."providerPaymentId", payment."providerPreferenceId", payment."checkoutURL",
		payment."receiptURL", payment."checkoutKey", payment."paidAt", payment."createdAt", payment."updatedAt"
		FROM asaas_payment_links link JOIN payments payment ON payment.id = link."paymentId"
		WHERE link."linkId" = $1 AND payment.provider = 'asaas'`, linkID))
}

// SwitchAsaasCheckout points an open invoice to the link of another payment
// method. The previous link stays mapped and payable.
func SwitchAsaasCheckout(paymentID, companyID int, checkoutKey, linkID, checkoutURL string) (Payment, error) {
	return scanPayment(db.QueryRow(`UPDATE payments SET "providerPreferenceId" = $1, "checkoutURL" = $2, "updatedAt" = now()
		WHERE id = $3 AND "companyId" = $4 AND provider = 'asaas' AND status = 'pending'
			AND "providerPaymentId" IS NULL AND "checkoutCreating" = false AND "checkoutKey" = $5
			AND "checkoutURL" IS NOT NULL AND "manualConfirmedAt" IS NULL AND "ignoredAt" IS NULL
			AND EXISTS (SELECT 1 FROM asaas_payment_links WHERE "linkId" = $1 AND "paymentId" = $3)
		RETURNING id, "companyId", "subscriptionId", status, "amountCents", "dueDate", provider,
			"providerPaymentId", "providerPreferenceId", "checkoutURL", "receiptURL", "checkoutKey", "paidAt", "createdAt", "updatedAt"`,
		linkID, checkoutURL, paymentID, companyID, checkoutKey))
}
