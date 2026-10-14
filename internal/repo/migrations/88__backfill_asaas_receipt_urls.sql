-- +goose Up
-- Asaas charges settled before the webhook fell back to the charge invoice page
-- kept no receipt when transactionReceiptUrl was empty. The invoice page lives
-- on the same host as the payment link, at /i/<charge id without "pay_">.
UPDATE public.payments
SET "receiptURL" = substring("checkoutURL" from '^(https://[^/]+)/') || '/i/' || substring("providerPaymentId" from 5),
    "updatedAt" = now()
WHERE provider = 'asaas'
  AND status IN ('paid', 'refunded')
  AND "receiptURL" IS NULL
  AND "providerPaymentId" LIKE 'pay\_%'
  AND length("providerPaymentId") > 4
  AND "checkoutURL" ~ '^https://(sandbox|www)\.asaas\.com/';

-- +goose Down
-- Backfilled receipts match what the webhook now stores, so they are kept.
SELECT 1;
