package repo

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// GenerateDueRenewalPayments creates local invoices only within their ten-day
// window. Each company is locked again by CreatePaymentForCurrentSubscription
// so concurrent workers cannot issue duplicate invoices.
func GenerateDueRenewalPayments(ctx context.Context, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT subscription."companyId"
		FROM subscriptions subscription
		JOIN companies company ON company.id = subscription."companyId"
		WHERE company."isActive" = true
		AND subscription.status IN ('trialing', 'active', 'past_due')
		AND subscription."priceCents" > 0
		AND subscription."currentPeriodEnd" <= $1`, now.AddDate(0, 0, renewalInvoiceLeadDays))
	if err != nil {
		return 0, err
	}
	var companyIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		companyIDs = append(companyIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	created := 0
	var failures []error
	for _, id := range companyIDs {
		if err := ctx.Err(); err != nil {
			return created, err
		}
		provider, err := ResolveCompanyPaymentProvider(id)
		if err == nil {
			_, added, createErr := CreatePaymentForCurrentSubscription(id, provider)
			if createErr == nil {
				if added {
					created++
				}
				continue
			}
			if errors.Is(createErr, ErrRenewalNotDue) {
				continue
			}
			err = createErr
		}
		failures = append(failures, fmt.Errorf("company %d: %w", id, err))
	}
	return created, errors.Join(failures...)
}
